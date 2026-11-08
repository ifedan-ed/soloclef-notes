"""Read an imported paper's best available text.

PubMed: the PubMed Central open-access XML when the paper is in that subset,
else the abstract. arXiv: the PDF through the upload parser. medRxiv/bioRxiv:
the PDF, then the servers' JATS XML if the PDF is refused, then the abstract.
Scholar: the linked open-access PDF, else the landing page through the web
crawler. Every document opens with a citation block naming which it got.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from typing import Any
from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

from ..capability_runner import CapabilityRunner
from ..ingest.fetched import FetchedContent
from ..ingest.remote_file import fetch_remote_file
from ..ingest.web_page import fetch_web_page
from .arxiv import fetch_entry, pdf_url
from .citation_header import citation_header, with_sections
from .clients import DEFAULT_CLIENTS, AcademicClients
from .jats import abstract_text, article_element, body_markdown, has_body_text
from .paper import PaperRef
from .pmc import find_pmcid, open_full_text
from .preprints import fetch_details, pdf_from_jats_url
from .pubmed import efetch_url, parse_efetch
from .xml_text import parse_xml

logger = logging.getLogger(__name__)

FileFetcher = Callable[..., Awaitable[FetchedContent | None]]
PageFetcher = Callable[[CapabilityRunner, str], Awaitable[FetchedContent | None]]


class PaperReader:
    def __init__(
        self,
        session: AsyncSession,
        runner: CapabilityRunner,
        user_id: UUID | str,
        *,
        clients: AcademicClients = DEFAULT_CLIENTS,
        file_fetcher: FileFetcher = fetch_remote_file,
        page_fetcher: PageFetcher = fetch_web_page,
    ) -> None:
        self._session = session
        self._runner = runner
        self._user_id = user_id
        self._clients = clients
        self._file = file_fetcher
        self._page = page_fetcher

    async def read(self, metadata: dict[str, Any], title: str) -> FetchedContent | None:
        paper = PaperRef.model_validate(metadata.get("paper") or {})
        source = metadata.get("research_source") or ""
        url = metadata.get("url") or ""
        readers = {
            "pubmed": self._pubmed,
            "arxiv": self._arxiv,
            "medrxiv": self._preprint,
            "google_scholar": self._scholar,
        }
        reader = readers.get(source)
        if reader is None:
            return None
        kind, sections, better_title = await reader(paper, url)
        if kind is None:
            return None
        title = better_title or title
        header = citation_header(title, paper, source=source, full_text=kind, url=url)
        return FetchedContent(
            markdown=with_sections(header, *sections),
            title=title,
            metadata={"full_text_source": kind},
        )

    async def _pdf(self, url: str | None, name: str) -> str | None:
        if not url:
            return None
        try:
            fetched = await self._file(self._session, url, self._user_id, name=name)
        except Exception:
            logger.info("reading paper PDF %s failed", url, exc_info=True)
            return None
        return fetched.markdown if fetched else None

    async def _pubmed(self, paper: PaperRef, url: str):
        fetch = self._clients.fetch_text
        if not paper.pmid:
            return None, (), None
        records = parse_efetch(await fetch(efetch_url([paper.pmid])))
        record = next((r for r in records if r.pmid == paper.pmid), None)
        abstract = record.abstract if record else ""
        pmcid = paper.pmcid or (record.pmcid if record else None)
        if not pmcid:
            pmcid = await find_pmcid(paper.pmid, fetch)
        article = (await open_full_text([pmcid], fetch)).get(pmcid) if pmcid else None
        title = record.title if record else None
        if article is not None:
            return (
                "pmc_oa_xml",
                (
                    ("Abstract", abstract or abstract_text(article)),
                    ("", body_markdown(article)),
                ),
                title,
            )
        return "abstract_only", (("Abstract", abstract),), title

    async def _arxiv(self, paper: PaperRef, url: str):
        if not paper.arxiv_id:
            return None, (), None
        text = await self._pdf(
            paper.pdf_url or pdf_url(paper.arxiv_id),
            f"{paper.arxiv_id.replace('/', '_')}.pdf",
        )
        if text:
            return "pdf", (("", text),), None
        entry = await fetch_entry(paper.arxiv_id, self._clients.fetch_text)
        if entry is None:
            return None, (), None
        return "abstract_only", (("Abstract", entry.summary),), entry.title

    async def _preprint(self, paper: PaperRef, url: str):
        if not paper.doi or not paper.server:
            return None, (), None
        fetch = self._clients.fetch_text
        details = await fetch_details(paper.server, paper.doi, fetch)
        jats_url = details.jats_url if details else None
        name = f"{paper.doi.rsplit('/', 1)[-1]}.pdf"
        text = await self._pdf(
            paper.pdf_url or pdf_from_jats_url(paper.server, jats_url), name
        )
        title = details.title if details else None
        if text:
            return "pdf", (("", text),), title
        if jats_url:
            try:
                article = article_element(parse_xml(await fetch(jats_url)))
            except Exception:
                logger.info("reading preprint JATS %s failed", jats_url, exc_info=True)
                article = None
            if article is not None and has_body_text(article):
                abstract = details.abstract if details else abstract_text(article)
                return (
                    "preprint_jats_xml",
                    (("Abstract", abstract), ("", body_markdown(article))),
                    title,
                )
        if details and details.abstract:
            return "abstract_only", (("Abstract", details.abstract),), title
        return None, (), None

    async def _scholar(self, paper: PaperRef, url: str):
        text = await self._pdf(paper.pdf_url, "paper.pdf")
        if text:
            return "pdf", (("", text),), None
        try:
            page = await self._page(self._runner, url)
        except Exception:
            logger.info("crawling Scholar result %s failed", url, exc_info=True)
            page = None
        if page is None:
            return None, (), None
        return "landing_page", (("", page.markdown),), None
