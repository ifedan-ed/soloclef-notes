"""Answer a research query from one academic source.

Fast asks for about ten papers. Deep asks for about thirty and, for PubMed,
reads the open-access full text of the top PubMed Central papers so their
snippet quotes the paper instead of the abstract's opening lines (arXiv and the
preprint servers already return the whole abstract, and Scholar has nothing
more to read without scraping publisher sites).
"""

from __future__ import annotations

from ..schemas import ResearchSource, ResultRow
from ..snippets import excerpt
from ..source_errors import SourceUnavailableError
from ..sources.rows import keep
from .arxiv import entry_row, search_arxiv
from .clients import AcademicClients
from .identifiers import dedupe_papers
from .jats import body_markdown
from .pmc import open_full_text
from .polite_http import FetchFailedError
from .preprints import preprint_row, search_preprints
from .pubmed import record_row, search_pubmed
from .scholar.parse import hit_row
from .scholar.search import search_scholar

ACADEMIC_SOURCES: frozenset[str] = frozenset(
    {"pubmed", "arxiv", "medrxiv", "google_scholar"}
)
FAST_LIMIT = 10
DEEP_LIMIT = 30
FULL_TEXTS_TO_READ = 5
SOURCE_BUSY = "{name} didn't answer just now. Try again in a minute."
_NAMES = {
    "pubmed": "PubMed",
    "arxiv": "arXiv",
    "medrxiv": "medRxiv / bioRxiv",
    "google_scholar": "Google Scholar",
}


def is_academic(source: ResearchSource) -> bool:
    return source in ACADEMIC_SOURCES


def limit_for(deep: bool) -> int:
    return DEEP_LIMIT if deep else FAST_LIMIT


async def _with_pmc_full_text(
    rows: list[ResultRow], query: str, clients: AcademicClients
) -> list[ResultRow]:
    targets = [row.paper.pmcid for row in rows if row.paper and row.paper.pmcid][
        :FULL_TEXTS_TO_READ
    ]
    articles = await open_full_text(targets, clients.fetch_text)
    out = []
    for row in rows:
        article = (
            articles.get(row.paper.pmcid) if row.paper and row.paper.pmcid else None
        )
        passage = excerpt(body_markdown(article), query) if article is not None else ""
        out.append(row.model_copy(update={"snippet": passage}) if passage else row)
    return out


async def search_academic(
    source: ResearchSource, query: str, *, deep: bool, clients: AcademicClients
) -> list[ResultRow]:
    try:
        return await _search(source, query, deep=deep, clients=clients)
    except FetchFailedError as exc:
        raise SourceUnavailableError(
            SOURCE_BUSY.format(name=_NAMES.get(source, source)), code="source_busy"
        ) from exc


async def _search(
    source: ResearchSource, query: str, *, deep: bool, clients: AcademicClients
) -> list[ResultRow]:
    limit = limit_for(deep)
    fetch = clients.fetch_text
    if source == "pubmed":
        rows = keep(record_row(r) for r in await search_pubmed(query, limit, fetch))
        if deep:
            rows = await _with_pmc_full_text(rows, query, clients)
    elif source == "arxiv":
        rows = keep(entry_row(e) for e in await search_arxiv(query, limit, fetch))
    elif source == "medrxiv":
        rows = keep(
            preprint_row(p) for p in await search_preprints(query, limit, fetch)
        )
    elif source == "google_scholar":
        hits = await search_scholar(query, limit, clients.fetch_scholar_page)
        rows = keep(hit_row(h) for h in hits)
    else:
        raise ValueError(f"{source} is not an academic source")
    return dedupe_papers(rows)[:limit]
