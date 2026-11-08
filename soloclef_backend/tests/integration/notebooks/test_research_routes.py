"""Notebook web research over HTTP: the streamed search and the import of picked results.

Scrapers, SearXNG and the Celery dispatch are the external boundaries; they are
replaced through the slice's dependency seams. Everything else is real.
"""

from __future__ import annotations

import json
from pathlib import Path
from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI
from sqlalchemy import select

from app.auth.context import AuthContext
from app.capabilities.core.access.rest import build_capabilities_router
from app.capabilities.core.runs import record_run
from app.capabilities.google_search.scrape.schemas import ScrapeOutput as SerpOutput
from app.capabilities.web.crawl.schemas import CrawlItem, CrawlOutput
from app.db import (
    Document,
    DocumentType,
    Run,
    SearchSourceConnector,
    SearchSourceConnectorType,
    get_async_session,
)
from app.notebooks.persistence import NotebookSource
from app.notebooks.research import seams
from app.notebooks.research.academic.clients import AcademicClients
from app.notebooks.research.web_apis.requests import ApiReply
from app.notebooks.routes import router as notebook_router
from app.users import get_auth_context

pytestmark = pytest.mark.integration

PAGE = "Solar panels turn sunlight into electricity using photovoltaic cells."
FIXTURES = Path(__file__).parents[2] / "unit" / "notebooks" / "research" / "fixtures"


def _serp(count: int) -> SerpOutput:
    return SerpOutput.model_validate(
        {
            "items": [
                {
                    "organicResults": [
                        {
                            "title": f"Result {i}",
                            "url": f"https://site{i}.example/solar",
                            "description": "teaser",
                        }
                        for i in range(count)
                    ]
                }
            ]
        }
    )


@pytest.fixture
def boundary():
    """Fake scrapers keyed by capability name, plus a record of what ran."""
    state = SimpleNamespace(calls=[], dispatched=[], outputs={})

    def executor(name):
        async def run(payload):
            state.calls.append((name, payload))
            output = state.outputs[name]
            return output(payload) if callable(output) else output

        return run

    state.outputs["google_search.scrape"] = _serp(12)
    state.outputs["web.crawl"] = lambda payload: CrawlOutput(
        items=[
            CrawlItem(url=url, status="success", markdown=PAGE)
            for url in payload.startUrls
        ]
    )
    state.executors = {
        name: executor(name) for name in ("google_search.scrape", "web.crawl")
    }
    state.academic_urls = []

    async def fetch_text(url):
        state.academic_urls.append(url)
        if "esearch.fcgi" in url:
            return _fixture("pubmed_esearch.json")
        if "efetch.fcgi" in url and "db=pubmed" in url:
            return _fixture("pubmed_efetch.xml")
        raise AssertionError(f"unexpected academic fetch {url}")

    state.academic = AcademicClients(fetch_text=fetch_text)
    # Exa/Tavily HTTP APIs: every request sent, answered with ``api_reply``.
    state.api_sent = []
    state.api_reply = ApiReply(200, {"results": []})

    async def send_api(request):
        state.api_sent.append(request)
        return state.api_reply

    state.send_api = send_api
    return state


def _fixture(name: str) -> str:
    return (FIXTURES / name).read_text(encoding="utf-8")


@pytest_asyncio.fixture
async def api(db_session, boundary):
    app = FastAPI()
    app.include_router(notebook_router, prefix="/api/v1")
    # The runs history routes only, to read research runs back as the UI does.
    app.include_router(build_capabilities_router(capabilities=[]), prefix="/api/v1")
    acting = SimpleNamespace(auth=None)

    async def session_override():
        yield db_session

    async def no_searxng(query, page):
        return None

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[seams.get_capability_executors] = lambda: {
        **boundary.executors
    }
    app.dependency_overrides[seams.get_searxng_page] = lambda: no_searxng
    app.dependency_overrides[seams.get_academic_clients] = lambda: boundary.academic
    app.dependency_overrides[seams.get_web_api_sender] = lambda: boundary.send_api

    async def record_in_test_session(**kwargs):
        return await record_run(db_session, **kwargs)

    app.dependency_overrides[seams.get_run_recorder] = lambda: record_in_test_session
    app.dependency_overrides[seams.get_import_dispatcher] = lambda: (
        lambda ids, workspace_id, user_id: boundary.dispatched.append(
            (list(ids), workspace_id, user_id)
        )
    )
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth: AuthContext):
            acting.auth = auth
            return client

        yield as_user


def _events(response: httpx.Response) -> list[dict]:
    return [
        json.loads(line[len("data: ") :])
        for line in response.text.split("\n\n")
        if line.startswith("data: ")
    ]


def _search_url(workspace_id: int, notebook_id: int) -> str:
    return f"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/research/search"


def _import_url(workspace_id: int, notebook_id: int) -> str:
    return f"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/research/import"


# --- search -----------------------------------------------------------------


async def test_fast_search_streams_phases_then_results_then_done(
    api, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(owner.auth).post(
        _search_url(owner.workspace.id, notebook.id),
        json={"query": "  solar panels ", "source": "web", "mode": "fast"},
    )
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/event-stream")
    assert response.headers["cache-control"] == "no-cache"
    assert response.headers["x-accel-buffering"] == "no"

    events = _events(response)
    assert events[0] == {"type": "phase", "phase": "searching", "progress": 5}
    assert [e["type"] for e in events][-2:] == ["results", "done"]
    results = events[-2]["results"]
    assert len(results) == 10
    assert set(results[0]) == {
        "id",
        "url",
        "title",
        "snippet",
        "source",
        "thumbnail_url",
        "favicon_url",
        "meta",
        "paper",
    }
    assert results[0]["source"] == "web"
    assert results[0]["favicon_url"].startswith("/api/v1/research/thumbnails?u=")
    assert [name for name, _ in boundary.calls] == ["google_search.scrape"]
    assert boundary.calls[0][1].queries == ["solar panels"]


async def test_deep_search_reads_the_top_pages(
    api, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(owner.auth).post(
        _search_url(owner.workspace.id, notebook.id),
        json={"query": "solar panels", "source": "google_search", "mode": "deep"},
    )
    events = _events(response)
    reading = [e for e in events if e.get("phase") == "reading"]
    assert len(reading) == 6
    crawled = [
        payload.startUrls[0] for name, payload in boundary.calls if name == "web.crawl"
    ]
    assert len(crawled) == 5
    results = events[-2]["results"]
    assert [r["snippet"] for r in results[:5]] == [PAGE] * 5
    assert results[5]["snippet"] == "teaser"


async def test_scraper_failure_ends_the_stream_with_an_error(
    api, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)

    def broken(payload):
        raise RuntimeError("upstream exploded")

    boundary.outputs["google_search.scrape"] = broken
    events = _events(
        await api(owner.auth).post(
            _search_url(owner.workspace.id, notebook.id),
            json={"query": "solar", "source": "google_search"},
        )
    )
    assert events[-1]["type"] == "error"
    assert "exploded" not in events[-1]["message"]


async def test_unknown_source_is_rejected(api, make_owner, make_notebook):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(owner.auth).post(
        _search_url(owner.workspace.id, notebook.id),
        json={"query": "solar", "source": "bing", "mode": "fast"},
    )
    assert response.status_code == 422


async def test_notebook_from_another_workspace_is_not_found(
    api, make_owner, make_notebook
):
    owner = await make_owner()
    outsider = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    client = api(outsider.auth)
    search = await client.post(
        _search_url(outsider.workspace.id, notebook.id), json={"query": "solar"}
    )
    imported = await client.post(
        _import_url(outsider.workspace.id, notebook.id),
        json={"items": [{"url": "https://e.com", "title": "E"}]},
    )
    assert (search.status_code, imported.status_code) == (404, 404)


async def test_non_member_is_refused(api, make_owner, make_notebook):
    owner = await make_owner()
    outsider = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(outsider.auth).post(
        _search_url(owner.workspace.id, notebook.id), json={"query": "solar"}
    )
    assert response.status_code == 403


# --- import -----------------------------------------------------------------


async def test_import_creates_pending_sources_and_queues_the_reading(
    api, db_session, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(owner.auth).post(
        _import_url(owner.workspace.id, notebook.id),
        json={
            "items": [
                {
                    "url": "https://site1.example/solar",
                    "title": "Solar 101",
                    "snippet": "teaser",
                    "source": "web",
                },
                {
                    "url": "https://www.youtube.com/watch?v=abc",
                    "title": "Solar video",
                    "source": "youtube",
                },
                # Same page as the first item: imported once.
                {"url": "http://www.site1.example/solar/", "title": "Dup"},
            ]
        },
    )
    assert response.status_code == 200, response.text
    listed = response.json()
    assert [(s["title"], s["status"], s["enabled"]) for s in listed] == [
        ("Solar 101", "pending", True),
        ("Solar video", "pending", True),
    ]
    assert [s["document_type"] for s in listed] == ["CRAWLED_URL", "YOUTUBE_VIDEO"]

    documents = (
        await db_session.scalars(
            select(Document).where(Document.workspace_id == owner.workspace.id)
        )
    ).all()
    assert {d.document_metadata["url"] for d in documents} == {
        "https://site1.example/solar",
        "https://www.youtube.com/watch?v=abc",
    }
    assert boundary.dispatched == [
        (
            sorted(d.id for d in documents),
            owner.workspace.id,
            owner.user.id,
        )
    ]

    # The same list the sources panel reads.
    sources = await api(owner.auth).get(
        f"/api/v1/workspaces/{owner.workspace.id}/notebooks/{notebook.id}/sources"
    )
    assert sources.json() == listed


async def test_import_links_a_document_the_workspace_already_has(
    api, db_session, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    existing = Document(
        title="Already saved",
        document_type=DocumentType.CRAWLED_URL,
        content="saved",
        source_markdown="saved",
        content_hash="h",
        document_metadata={"url": "https://site1.example/solar"},
        workspace_id=owner.workspace.id,
        status={"state": "ready"},
    )
    db_session.add(existing)
    await db_session.flush()
    notebook = await make_notebook(
        owner.workspace.id, sources=[("document", existing.id, False)]
    )

    response = await api(owner.auth).post(
        _import_url(owner.workspace.id, notebook.id),
        json={"items": [{"url": "https://site1.example/solar", "title": "Solar"}]},
    )
    assert response.status_code == 200
    [source] = response.json()
    assert (source["target_id"], source["status"], source["enabled"]) == (
        existing.id,
        "ready",
        True,
    )
    assert boundary.dispatched == []
    links = (
        await db_session.scalars(
            select(NotebookSource).where(NotebookSource.notebook_id == notebook.id)
        )
    ).all()
    assert [(link.document_id, link.enabled) for link in links] == [(existing.id, True)]


async def test_importing_twice_reuses_the_first_import(
    api, db_session, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    first = await make_notebook(owner.workspace.id, title="First")
    second = await make_notebook(owner.workspace.id, title="Second")
    item = {"items": [{"url": "https://site2.example/a?utm_source=x", "title": "A"}]}
    client = api(owner.auth)
    await client.post(_import_url(owner.workspace.id, first.id), json=item)
    again = await client.post(
        _import_url(owner.workspace.id, second.id),
        json={"items": [{"url": "https://site2.example/a", "title": "A"}]},
    )
    assert again.status_code == 200
    assert len(boundary.dispatched) == 1
    total = (
        await db_session.scalars(
            select(Document.id).where(Document.workspace_id == owner.workspace.id)
        )
    ).all()
    assert len(total) == 1


async def test_viewer_can_search_but_not_import(
    api, make_owner, make_notebook, make_user, add_member
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    viewer = await make_user()
    await add_member(owner.workspace, viewer, "Viewer")
    client = api(AuthContext.session(viewer))
    search = await client.post(
        _search_url(owner.workspace.id, notebook.id), json={"query": "solar"}
    )
    imported = await client.post(
        _import_url(owner.workspace.id, notebook.id),
        json={"items": [{"url": "https://e.com", "title": "E"}]},
    )
    assert (search.status_code, imported.status_code) == (200, 403)


async def test_import_refuses_non_http_urls(api, make_owner, make_notebook):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(owner.auth).post(
        _import_url(owner.workspace.id, notebook.id),
        json={"items": [{"url": "file:///etc/passwd", "title": "x"}]},
    )
    assert response.status_code == 422


# --- runs log ---------------------------------------------------------------


async def test_every_search_is_recorded_in_the_runs_log(
    api, db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    await api(owner.auth).post(
        _search_url(owner.workspace.id, notebook.id),
        json={"query": "solar panels", "source": "web", "mode": "fast"},
    )
    [run] = (
        await db_session.scalars(
            select(Run).where(Run.workspace_id == owner.workspace.id)
        )
    ).all()
    assert run.capability == "research.web"
    assert run.origin == "notebook"
    assert run.status == "success"
    assert run.item_count == 10
    assert run.input == {
        "query": "solar panels",
        "source": "web",
        "mode": "fast",
        "notebook_id": notebook.id,
    }
    assert run.duration_ms is not None
    assert run.user_id == owner.user.id

    # The API Access runs table reads it back like any scraper run.
    listed = await api(owner.auth).get(
        f"/api/v1/workspaces/{owner.workspace.id}/scrapers/runs"
    )
    assert listed.status_code == 200
    [summary] = listed.json()
    assert (summary["capability"], summary["origin"], summary["item_count"]) == (
        "research.web",
        "notebook",
        10,
    )


async def test_a_failed_search_is_recorded_as_an_error(
    api, db_session, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)

    def broken(payload):
        raise RuntimeError("upstream exploded")

    boundary.outputs["google_search.scrape"] = broken
    await api(owner.auth).post(
        _search_url(owner.workspace.id, notebook.id),
        json={"query": "solar", "source": "google_search"},
    )
    [run] = (
        await db_session.scalars(
            select(Run).where(Run.workspace_id == owner.workspace.id)
        )
    ).all()
    assert (run.capability, run.status) == ("research.google_search", "error")
    assert "exploded" not in (run.error or "")


# --- academic sources --------------------------------------------------------


async def test_pubmed_search_returns_papers_with_badges(
    api, db_session, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    events = _events(
        await api(owner.auth).post(
            _search_url(owner.workspace.id, notebook.id),
            json={"query": "bronchiolitis high flow nasal cannula", "source": "pubmed"},
        )
    )
    results = events[-2]["results"]
    assert len(results) == 5
    first = results[0]
    assert first["source"] == "pubmed"
    assert first["url"] == "https://pubmed.ncbi.nlm.nih.gov/38506440/"
    assert first["paper"]["pmid"] == "38506440"
    assert "pubmed" in first["paper"]["badges"]
    assert boundary.calls == []  # no scraper, no credit spent
    [run] = (
        await db_session.scalars(
            select(Run).where(Run.workspace_id == owner.workspace.id)
        )
    ).all()
    assert (run.capability, run.item_count, run.cost_micros) == (
        "research.pubmed",
        5,
        0,
    )


async def test_importing_a_paper_keeps_its_ids_and_reuses_it_from_another_source(
    api, db_session, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    client = api(owner.auth)
    first = await client.post(
        _import_url(owner.workspace.id, notebook.id),
        json={
            "items": [
                {
                    "url": "https://pubmed.ncbi.nlm.nih.gov/35721052/",
                    "title": "HFNC in children",
                    "source": "pubmed",
                    "paper": {
                        "pmid": "35721052",
                        "pmcid": "PMC9203852",
                        "doi": "10.3389/fmed.2022.920549",
                        "badges": ["pubmed", "pmc"],
                        "open_access": True,
                    },
                }
            ]
        },
    )
    assert first.status_code == 200, first.text
    [document] = (
        await db_session.scalars(
            select(Document).where(Document.workspace_id == owner.workspace.id)
        )
    ).all()
    assert document.document_metadata["paper"]["pmid"] == "35721052"
    assert document.document_metadata["research_source"] == "pubmed"

    # The same paper found on Scholar, at the publisher's URL: linked, not copied.
    other = await make_notebook(owner.workspace.id, title="Other")
    again = await client.post(
        _import_url(owner.workspace.id, other.id),
        json={
            "items": [
                {
                    "url": "https://www.frontiersin.org/articles/10.3389/fmed.2022.920549",
                    "title": "HFNC in children",
                    "source": "google_scholar",
                    "paper": {"doi": "10.3389/FMED.2022.920549", "badges": ["scholar"]},
                }
            ]
        },
    )
    assert [s["target_id"] for s in again.json()] == [document.id]
    assert len(boundary.dispatched) == 1


async def test_import_refuses_a_forged_paper_id(api, make_owner, make_notebook):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(owner.auth).post(
        _import_url(owner.workspace.id, notebook.id),
        json={
            "items": [
                {
                    "url": "https://pubmed.ncbi.nlm.nih.gov/1/",
                    "title": "x",
                    "source": "pubmed",
                    "paper": {"pmid": "1 OR 1=1"},
                }
            ]
        },
    )
    assert response.status_code == 422


# --- Exa / Tavily --------------------------------------------------------------


def _sources_url(workspace_id: int, notebook_id: int) -> str:
    return f"/api/v1/workspaces/{workspace_id}/notebooks/{notebook_id}/research/sources"


async def _connect_mcp(db_session, owner, name, url, headers):
    db_session.add(
        SearchSourceConnector(
            name=name,
            connector_type=SearchSourceConnectorType.MCP_CONNECTOR,
            config={
                "server_config": {
                    "url": url,
                    "transport": "streamable-http",
                    "headers": headers,
                }
            },
            workspace_id=owner.workspace.id,
            user_id=owner.user.id,
        )
    )
    await db_session.flush()


async def test_sources_status_reports_providers_without_a_key(
    api, make_owner, make_notebook, db_session
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    client = api(owner.auth)
    url = _sources_url(owner.workspace.id, notebook.id)

    none = await client.get(url)
    assert none.status_code == 200
    assert none.json() == {
        "unavailable": {"exa": "not_connected", "tavily": "not_connected"},
        "page_reader": "crawler",
    }

    await _connect_mcp(
        db_session, owner, "Exa", "https://mcp.exa.ai/mcp", {"x-api-key": "exa-secret"}
    )
    body = (await client.get(url)).json()
    assert body == {
        "unavailable": {"tavily": "not_connected"},
        "page_reader": "crawler",
    }
    assert "exa-secret" not in json.dumps(body)


async def test_sources_status_is_per_workspace(
    api, make_owner, make_notebook, db_session
):
    owner = await make_owner()
    outsider = await make_owner()
    await _connect_mcp(
        db_session,
        owner,
        "Tavily",
        "https://mcp.tavily.com/mcp/",
        {"Authorization": "Bearer tvly-owner"},
    )
    notebook = await make_notebook(outsider.workspace.id)
    body = (
        await api(outsider.auth).get(_sources_url(outsider.workspace.id, notebook.id))
    ).json()
    assert "tavily" in body["unavailable"]
    theirs = await make_notebook(owner.workspace.id)
    refused = await api(outsider.auth).get(
        _sources_url(outsider.workspace.id, theirs.id)
    )
    assert refused.status_code == 404


async def test_tavily_search_uses_the_workspace_key_once(
    api, make_owner, make_notebook, boundary, db_session
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    await _connect_mcp(
        db_session,
        owner,
        "Tavily",
        "https://mcp.tavily.com/mcp/",
        {"Authorization": "Bearer tvly-owner"},
    )
    boundary.api_reply = ApiReply(
        200,
        {
            "results": [
                {"title": "Asthma", "url": "https://a.example/", "content": "Plans"}
            ]
        },
    )
    events = _events(
        await api(owner.auth).post(
            _search_url(owner.workspace.id, notebook.id),
            json={"query": "asthma", "source": "tavily", "mode": "deep"},
        )
    )
    assert [e["type"] for e in events][-2:] == ["results", "done"]
    [row] = events[-2]["results"]
    assert (row["source"], row["url"], row["snippet"]) == (
        "tavily",
        "https://a.example/",
        "Plans",
    )
    [sent] = boundary.api_sent
    assert sent.headers["Authorization"] == "Bearer tvly-owner"
    assert sent.body["search_depth"] == "advanced"
    # Deep mode does not re-read provider results with the crawler.
    assert boundary.calls == []


async def test_exa_search_without_a_key_is_an_inline_error(
    api, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    events = _events(
        await api(owner.auth).post(
            _search_url(owner.workspace.id, notebook.id),
            json={"query": "asthma", "source": "exa"},
        )
    )
    assert events[-1]["type"] == "error"
    assert events[-1]["code"] == "source_not_connected"
    assert boundary.api_sent == []


# --- several sources at once ------------------------------------------------------


def _runs_by_capability(runs) -> dict:
    return {run.capability: run for run in runs}


async def test_several_sources_are_searched_together_and_merged(
    api, db_session, make_owner, make_notebook, boundary, monkeypatch
):
    from app.config import config

    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    client = api(owner.auth)
    # The same search of Google alone, for what it costs on its own.
    await client.post(
        _search_url(owner.workspace.id, notebook.id),
        json={"query": "bronchiolitis", "source": "google_search"},
    )
    [alone] = (
        await db_session.scalars(
            select(Run).where(Run.workspace_id == owner.workspace.id)
        )
    ).all()
    alone_cost = alone.cost_micros
    await db_session.delete(alone)
    await db_session.flush()

    response = await client.post(
        _search_url(owner.workspace.id, notebook.id),
        json={
            "query": "bronchiolitis",
            "sources": ["google_search", "pubmed"],
            "mode": "fast",
        },
    )
    assert response.status_code == 200
    events = _events(response)
    providers = [
        (e["source"], e["status"], e.get("count"))
        for e in events
        if e["type"] == "provider"
    ]
    assert providers[:2] == [
        ("google_search", "searching", None),
        ("pubmed", "searching", None),
    ]
    assert sorted(providers[2:]) == [
        ("google_search", "done", 10),
        ("pubmed", "done", 5),
    ]
    assert [e["type"] for e in events][-2:] == ["results", "done"]
    results = events[-2]["results"]
    assert len(results) == 15
    assert [r["source"] for r in results[:4]] == [
        "google_search",
        "pubmed",
        "google_search",
        "pubmed",
    ]

    runs = _runs_by_capability(
        (
            await db_session.scalars(
                select(Run).where(Run.workspace_id == owner.workspace.id)
            )
        ).all()
    )
    assert set(runs) == {"research.google_search", "research.pubmed"}
    google, pubmed = runs["research.google_search"], runs["research.pubmed"]
    assert (google.status, google.item_count) == ("success", 10)
    assert (pubmed.status, pubmed.item_count, pubmed.cost_micros) == ("success", 5, 0)
    # Each source is billed on its own, exactly as a search of it alone.
    assert alone_cost > 0
    assert google.cost_micros == alone_cost
    assert google.input["sources"] == ["google_search", "pubmed"]


async def test_a_failing_source_leaves_the_others_results(
    api, db_session, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)

    def broken(payload):
        raise RuntimeError("upstream exploded")

    boundary.outputs["google_search.scrape"] = broken
    events = _events(
        await api(owner.auth).post(
            _search_url(owner.workspace.id, notebook.id),
            json={
                "query": "bronchiolitis",
                "sources": ["google_search", "pubmed", "exa"],
            },
        )
    )
    failed = {
        e["source"]: e["code"]
        for e in events
        if e["type"] == "provider" and e["status"] == "error"
    }
    assert failed == {"google_search": "search_failed", "exa": "source_not_connected"}
    assert "exploded" not in json.dumps(events)
    assert events[-1] == {"type": "done"}
    assert {r["source"] for r in events[-2]["results"]} == {"pubmed"}
    assert boundary.api_sent == []

    runs = _runs_by_capability(
        (
            await db_session.scalars(
                select(Run).where(Run.workspace_id == owner.workspace.id)
            )
        ).all()
    )
    assert {name: run.status for name, run in runs.items()} == {
        "research.google_search": "error",
        "research.pubmed": "success",
        "research.exa": "error",
    }


async def test_when_every_source_fails_the_search_is_an_error(
    api, make_owner, make_notebook, boundary
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    events = _events(
        await api(owner.auth).post(
            _search_url(owner.workspace.id, notebook.id),
            json={"query": "asthma", "sources": ["exa", "tavily"]},
        )
    )
    assert events[-1]["type"] == "error"
    assert (events[-1]["code"], events[-1]["source"]) == ("source_not_connected", "exa")


async def test_more_than_four_sources_are_refused(api, make_owner, make_notebook):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(owner.auth).post(
        _search_url(owner.workspace.id, notebook.id),
        json={
            "query": "asthma",
            "sources": ["web", "pubmed", "arxiv", "medrxiv", "reddit"],
        },
    )
    assert response.status_code == 422


async def test_several_sources_need_the_same_access_as_one(
    api, make_owner, make_notebook
):
    owner = await make_owner()
    outsider = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await api(outsider.auth).post(
        _search_url(owner.workspace.id, notebook.id),
        json={"query": "solar", "sources": ["web", "pubmed"]},
    )
    assert response.status_code == 403
