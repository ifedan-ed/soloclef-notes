"""Standalone academic search over HTTP: one source, no notebook needed.

Built for the MCP server's academic search tools. Reuses the same academic
clients seam as notebook research search, so the real network is never hit.
"""

from __future__ import annotations

from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI

from app.auth.context import AuthContext
from app.db import get_async_session
from app.notebooks.research import seams
from app.notebooks.research.academic.clients import AcademicClients
from app.notebooks.routes import router as notebook_router
from app.users import get_auth_context

pytestmark = pytest.mark.integration

FIXTURES = __import__("pathlib").Path(__file__).parents[2] / "unit" / "notebooks" / "research" / "fixtures"


def _fixture(name: str) -> str:
    return (FIXTURES / name).read_text(encoding="utf-8")


@pytest.fixture
def boundary():
    state = SimpleNamespace(urls=[])

    async def fetch_text(url):
        state.urls.append(url)
        if "esearch.fcgi" in url:
            return _fixture("pubmed_esearch.json")
        if "efetch.fcgi" in url and "db=pubmed" in url:
            return _fixture("pubmed_efetch.xml")
        if "export.arxiv.org" in url:
            return _fixture("arxiv_query.xml")
        if "europepmc" in url:
            return _fixture("epmc_preprints.json")
        raise AssertionError(f"unexpected academic fetch {url}")

    state.academic = AcademicClients(fetch_text=fetch_text)
    return state


@pytest_asyncio.fixture
async def api(db_session, boundary):
    app = FastAPI()
    app.include_router(notebook_router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[seams.get_academic_clients] = lambda: boundary.academic

    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth: AuthContext):
            acting.auth = auth
            return client

        yield as_user


def _url(workspace_id: int, source: str) -> str:
    return f"/api/v1/workspaces/{workspace_id}/academic-search/{source}"


async def test_pubmed_search_returns_rows_with_paper_identifiers(
    api, make_owner, boundary
):
    owner = await make_owner()
    response = await api(owner.auth).post(
        _url(owner.workspace.id, "pubmed"), json={"query": "bronchiolitis hfnc"}
    )
    assert response.status_code == 200
    rows = response.json()
    assert rows
    first = rows[0]
    assert first["source"] == "pubmed"
    assert first["paper"]["pmid"]
    assert first["title"]
    assert first["snippet"]


async def test_arxiv_search_returns_rows(api, make_owner, boundary):
    owner = await make_owner()
    response = await api(owner.auth).post(
        _url(owner.workspace.id, "arxiv"), json={"query": "transformers"}
    )
    assert response.status_code == 200
    rows = response.json()
    assert rows
    assert rows[0]["source"] == "arxiv"
    assert rows[0]["paper"]["arxiv_id"]


async def test_preprint_search_filters_by_server(api, make_owner, boundary):
    owner = await make_owner()
    medrxiv = await api(owner.auth).post(
        _url(owner.workspace.id, "medrxiv"),
        json={"query": "asthma", "server": "medrxiv"},
    )
    assert medrxiv.status_code == 200
    rows = medrxiv.json()
    assert rows
    assert all(r["paper"]["server"] == "medrxiv" for r in rows)

    biorxiv = await api(owner.auth).post(
        _url(owner.workspace.id, "medrxiv"),
        json={"query": "asthma", "server": "biorxiv"},
    )
    assert biorxiv.status_code == 200
    assert biorxiv.json() == []


async def test_pubmed_date_window_is_folded_into_the_query(api, make_owner, boundary):
    owner = await make_owner()
    response = await api(owner.auth).post(
        _url(owner.workspace.id, "pubmed"),
        json={"query": "asthma", "since": 2020, "until": 2024},
    )
    assert response.status_code == 200
    import urllib.parse

    esearch_urls = [u for u in boundary.urls if "esearch.fcgi" in u]
    assert esearch_urls
    decoded = urllib.parse.unquote(esearch_urls[0])
    assert '"2020"[pdat] : "2024"[pdat]' in decoded


async def test_max_results_caps_the_response(api, make_owner, boundary):
    owner = await make_owner()
    response = await api(owner.auth).post(
        _url(owner.workspace.id, "pubmed"), json={"query": "x", "max_results": 2}
    )
    assert response.status_code == 200
    assert len(response.json()) <= 2


async def test_unknown_source_is_rejected(api, make_owner):
    owner = await make_owner()
    response = await api(owner.auth).post(
        _url(owner.workspace.id, "bing"), json={"query": "x"}
    )
    assert response.status_code == 422
