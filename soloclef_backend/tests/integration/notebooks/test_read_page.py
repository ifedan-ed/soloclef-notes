"""Read a web page: paste a URL, Browserbase renders it, it becomes a source.

The route, persistence, the fill job and the key lookup are real (Postgres);
Browserbase is the fake at the HTTP boundary and its browser the stand-in in
``tests/e2e/fakes/browserbase.py``.
"""

from __future__ import annotations

from functools import partial
from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI
from sqlalchemy import select

from app.db import (
    Document,
    SearchSourceConnector,
    SearchSourceConnectorType,
    get_async_session,
)
from app.notebooks.persistence import NotebookSource
from app.notebooks.research import seams
from app.notebooks.research.capability_runner import CapabilityRunner
from app.notebooks.research.import_job import fill_document
from app.notebooks.research.page_read.browserbase_api import BrowserbaseApi
from app.notebooks.research.page_read.reader import read_web_page
from app.notebooks.routes import router as notebook_router
from app.users import get_auth_context
from tests.e2e.fakes.browserbase import (
    E2E_JS_PAGE_HTML,
    E2E_JS_PAGE_TEXT,
    E2E_JS_PAGE_TITLE,
    E2E_KEY,
    FakeBrowser,
    FakeBrowserbase,
)

pytestmark = pytest.mark.integration

URL = "https://93.184.215.14/e2e-js-page"


@pytest_asyncio.fixture
async def api(db_session):
    app = FastAPI()
    app.include_router(notebook_router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None, dispatched=[])

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[seams.get_page_read_dispatcher] = lambda: (
        lambda ids, workspace_id, user_id: acting.dispatched.append(
            (list(ids), workspace_id, user_id)
        )
    )
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:
        acting.client = client
        yield acting


def _read_url(owner, notebook) -> str:
    return (
        f"/api/v1/workspaces/{owner.workspace.id}/notebooks/{notebook.id}"
        "/research/read-page"
    )


async def _browserbase_connector(db_session, owner) -> None:
    db_session.add(
        SearchSourceConnector(
            name="Browserbase",
            connector_type=SearchSourceConnectorType.MCP_CONNECTOR,
            is_indexable=False,
            config={
                "server_config": {
                    "url": "https://mcp.browserbase.com/mcp",
                    "transport": "streamable-http",
                    "headers": {"Authorization": f"Bearer {E2E_KEY}"},
                }
            },
            workspace_id=owner.workspace.id,
            user_id=owner.user.id,
        )
    )
    await db_session.flush()


async def test_a_pasted_page_becomes_a_pending_source_read_in_the_background(
    api, db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    api.auth = owner.auth

    response = await api.client.post(_read_url(owner, notebook), json={"url": URL})

    assert response.status_code == 200, response.text
    [source] = response.json()
    assert source["title"] == URL and source["kind"] == "document"
    [(ids, workspace_id, user_id)] = api.dispatched
    assert user_id == owner.user.id
    assert ids == [source["target_id"]] and workspace_id == owner.workspace.id
    link = await db_session.scalar(
        select(NotebookSource).where(NotebookSource.notebook_id == notebook.id)
    )
    assert link.enabled


@pytest.mark.parametrize(
    "url",
    [
        "http://127.0.0.1:8000/admin",
        "http://169.254.169.254/latest/meta-data/",
        "http://10.0.0.8/",
    ],
)
async def test_private_addresses_are_refused(api, make_owner, make_notebook, url):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    api.auth = owner.auth
    response = await api.client.post(_read_url(owner, notebook), json={"url": url})
    assert response.status_code == 422
    assert "public web page" in response.json()["detail"]
    assert api.dispatched == []


async def test_only_http_links_are_accepted(api, make_owner, make_notebook):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    api.auth = owner.auth
    response = await api.client.post(
        _read_url(owner, notebook), json={"url": "file:///etc/passwd"}
    )
    assert response.status_code == 422


async def test_someone_else_cannot_add_to_the_notebook(api, make_owner, make_notebook):
    owner = await make_owner()
    stranger = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    api.auth = stranger.auth
    response = await api.client.post(_read_url(owner, notebook), json={"url": URL})
    assert response.status_code in (403, 404)


async def test_the_status_says_browserbase_once_it_is_connected(
    api, db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    api.auth = owner.auth
    status_url = (
        f"/api/v1/workspaces/{owner.workspace.id}/notebooks/{notebook.id}"
        "/research/sources"
    )
    assert (await api.client.get(status_url)).json()["page_reader"] == "crawler"
    await _browserbase_connector(db_session, owner)
    assert (await api.client.get(status_url)).json()["page_reader"] == "browserbase"


async def test_the_background_read_fills_the_source_with_the_rendered_page(
    api, db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    await _browserbase_connector(db_session, owner)
    api.auth = owner.auth
    [source] = (
        await api.client.post(_read_url(owner, notebook), json={"url": URL})
    ).json()

    fake = FakeBrowserbase()
    browser = FakeBrowser(pages={URL: (E2E_JS_PAGE_TITLE, E2E_JS_PAGE_HTML)})
    queued = []

    async def record(_session, _documents):
        return None

    runner = CapabilityRunner(db_session, owner.workspace.id, gate=False)
    await fill_document(
        db_session,
        source["target_id"],
        owner.user.id,
        runner,
        fetch=partial(
            read_web_page,
            api_factory=lambda key: BrowserbaseApi(
                key, base_url="https://bb.test", transport=fake.transport()
            ),
            loader=browser,
        ),
        record=record,
        queue_reindex=lambda doc_id, user: queued.append(doc_id),
    )

    document = await db_session.scalar(
        select(Document).where(Document.id == source["target_id"])
    )
    assert document.title == E2E_JS_PAGE_TITLE
    assert E2E_JS_PAGE_TEXT in document.content
    assert document.document_metadata["url"] == URL
    assert document.document_metadata["reader"] == "browserbase"
    assert document.document_metadata["fetched"] is True
    assert document.status == {"state": "ready"} or document.status["state"] == "ready"
    assert queued == [document.id]
    # The key came from the connector row; the session was ended.
    assert fake.requests[0].headers["x-bb-api-key"] == E2E_KEY
    assert fake.open_sessions == []
