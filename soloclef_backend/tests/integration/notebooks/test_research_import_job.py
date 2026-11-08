"""The background half of a research import: read the page, store it, queue indexing."""

from __future__ import annotations

from pathlib import Path

import pytest
from sqlalchemy import select

from app.capabilities.web.crawl.schemas import CrawlItem, CrawlOutput
from app.capabilities.youtube.scrape.schemas import ScrapeOutput as YouTubeOutput
from app.db import Document, DocumentType
from app.notebooks.research.capability_runner import CapabilityRunner
from app.notebooks.research.import_job import fill_document
from app.notebooks.research.import_service import import_items
from app.notebooks.research.schemas import ImportItem

pytestmark = pytest.mark.integration


async def _pending(db_session, owner, notebook, url, title="Solar 101", snippet=None):
    [document_id] = await import_items(
        db_session,
        notebook=notebook,
        items=[ImportItem(url=url, title=title, snippet=snippet)],
        user_id=owner.user.id,
    )
    await db_session.commit()
    return document_id


async def _fill(db_session, owner, document_id, executors):
    recorded, queued = [], []

    async def record(session, documents):
        recorded.extend(d.id for d in documents)

    runner = CapabilityRunner(db_session, owner.workspace.id, executors, gate=False)
    await fill_document(
        db_session,
        document_id,
        owner.user.id,
        runner,
        record=record,
        queue_reindex=lambda doc_id, user: queued.append((doc_id, user)),
    )
    document = await db_session.scalar(
        select(Document).where(Document.id == document_id)
    )
    return document, recorded, queued


async def test_crawled_page_becomes_the_content(db_session, make_owner, make_notebook):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    url = "https://site1.example/solar"
    document_id = await _pending(db_session, owner, notebook, url, title=url)

    async def crawl(payload):
        assert payload.startUrls == [url]
        return CrawlOutput(
            items=[
                CrawlItem(
                    url=url,
                    status="success",
                    markdown="# Solar\n\nPanels make power.",
                    metadata={"title": "Solar guide"},
                )
            ]
        )

    document, recorded, queued = await _fill(
        db_session, owner, document_id, {"web.crawl": crawl}
    )
    assert document.status == {"state": "ready"}
    assert document.source_markdown == "# Solar\n\nPanels make power."
    assert document.content_needs_reindexing is True
    assert document.title == "Solar guide"
    assert document.document_metadata["fetched"] is True
    assert document.document_type is DocumentType.CRAWLED_URL
    assert recorded == [document_id]
    assert queued == [(document_id, str(owner.user.id))]


async def test_failed_crawl_falls_back_to_title_and_snippet(
    db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    url = "https://site1.example/blocked"
    document_id = await _pending(
        db_session, owner, notebook, url, snippet="The search teaser."
    )

    async def crawl(payload):
        return CrawlOutput(items=[CrawlItem(url=url, status="failed", error="403")])

    document, _, queued = await _fill(
        db_session, owner, document_id, {"web.crawl": crawl}
    )
    assert document.status == {"state": "ready"}
    assert document.source_markdown == (
        f"# Solar 101\n\nThe search teaser.\n\nSource: {url}"
    )
    assert document.document_metadata["fetched"] is False
    assert queued == [(document_id, str(owner.user.id))]


async def test_crawler_crash_also_falls_back(db_session, make_owner, make_notebook):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    document_id = await _pending(
        db_session, owner, notebook, "https://site1.example/x", snippet="teaser"
    )

    async def crawl(payload):
        raise RuntimeError("browser died")

    document, _, _ = await _fill(db_session, owner, document_id, {"web.crawl": crawl})
    assert document.status == {"state": "ready"}
    assert "teaser" in document.source_markdown


async def test_youtube_video_is_read_with_its_transcript(
    db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    url = "https://www.youtube.com/watch?v=abc"
    document_id = await _pending(db_session, owner, notebook, url, title="Solar video")

    async def scrape(payload):
        assert payload.urls == [url] and payload.download_subtitles
        return YouTubeOutput.model_validate(
            {
                "items": [
                    {
                        "id": "abc",
                        "title": "Solar video",
                        "url": url,
                        "channelName": "Energy Now",
                        "text": "All about panels.",
                        "subtitles": [
                            {"srt": "1\n00:00:01,000 --> 00:00:02,000\nHello solar\n"}
                        ],
                    }
                ]
            }
        )

    document, _, _ = await _fill(
        db_session, owner, document_id, {"youtube.scrape": scrape}
    )
    assert document.document_type is DocumentType.YOUTUBE_VIDEO
    assert "## Transcript\n\nHello solar" in document.source_markdown
    assert document.document_metadata["video_id"] == "abc"
    assert document.document_metadata["channel_name"] == "Energy Now"
    assert document.status == {"state": "ready"}


# --- papers ------------------------------------------------------------------

_FIXTURES = Path(__file__).parents[2] / "unit" / "notebooks" / "research" / "fixtures"


def _recorded(name: str) -> str:
    return (_FIXTURES / name).read_text(encoding="utf-8")


async def test_a_pubmed_paper_is_filled_from_its_open_access_full_text(
    db_session, make_owner, make_notebook
):
    from app.notebooks.research.academic.clients import AcademicClients
    from app.notebooks.research.academic.ingest import PaperReader
    from app.notebooks.research.academic.paper import PaperRef

    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    [document_id] = await import_items(
        db_session,
        notebook=notebook,
        items=[
            ImportItem(
                url="https://pubmed.ncbi.nlm.nih.gov/35721052/",
                title="HFNC (from the result list)",
                source="pubmed",
                paper=PaperRef(pmid="35721052", badges=["pubmed"]),
            )
        ],
        user_id=owner.user.id,
    )
    await db_session.commit()

    async def fetch_text(url):
        if "db=pubmed" in url:
            return _recorded("pubmed_efetch.xml")
        if "db=pmc" in url:
            return _recorded("pmc_PMC9203852.xml")
        raise AssertionError(url)

    async def fetch_paper(session, runner, metadata, title, user_id):
        reader = PaperReader(
            session, runner, user_id, clients=AcademicClients(fetch_text=fetch_text)
        )
        return await reader.read(metadata, title)

    queued = []
    runner = CapabilityRunner(db_session, owner.workspace.id, {}, gate=False)

    async def record(session, documents):
        return None

    await fill_document(
        db_session,
        document_id,
        owner.user.id,
        runner,
        fetch_paper=fetch_paper,
        record=record,
        queue_reindex=lambda doc_id, user: queued.append(doc_id),
    )
    document = await db_session.scalar(
        select(Document).where(Document.id == document_id)
    )
    assert document.status == {"state": "ready"}
    assert document.document_metadata["full_text_source"] == "pmc_oa_xml"
    assert document.document_metadata["paper"]["pmid"] == "35721052"
    assert document.document_metadata["fetched"] is True
    assert "PubMed Central open-access full text" in document.source_markdown
    assert "## Introduction" in document.source_markdown
    # The record's own title replaces the list's shortened one.
    assert document.title != "HFNC (from the result list)"
    assert queued == [document_id]
