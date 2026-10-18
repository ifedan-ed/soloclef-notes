"""A stand-in Browserbase, at the HTTP boundary, plus a stand-in cloud browser.

``FakeBrowserbase`` answers Browserbase's REST API (``POST /v1/sessions``,
``POST /v1/sessions/{id}`` with ``REQUEST_RELEASE``, ``GET /v1/projects``) as
an ``httpx.MockTransport`` and records every session, so tests can prove each
one was ended. ``FakeBrowser`` stands in for the remote browser a session's
``connectUrl`` leads to: it renders from a table of pages and can time out or
wander off to a private address.

``install`` (the e2e worker) routes the real ``BrowserbaseApi`` through the
fake transport and the real reader through the fake browser; every workspace
counts as having Browserbase connected. Production code never imports this.
"""

from __future__ import annotations

import asyncio
import json
import uuid
from dataclasses import dataclass, field

import httpx

E2E_KEY = "bb_e2e_fake_key"
# A global IP literal: public for the URL guard with no DNS (the e2e stack is offline).
E2E_JS_PAGE_URL = "https://93.184.215.14/e2e-js-page"
E2E_JS_PAGE_TITLE = "E2E rendered page"
E2E_JS_PAGE_TEXT = (
    "This paragraph only exists after JavaScript runs: the Browserbase stand-in "
    "rendered it for the Read a web page flow."
)
E2E_JS_PAGE_HTML = f"""<!doctype html><html><head><title>{E2E_JS_PAGE_TITLE}</title></head>
<body><main><article><h1>{E2E_JS_PAGE_TITLE}</h1>
<p>{E2E_JS_PAGE_TEXT}</p>
<p>A second paragraph so the main-content extractor keeps the article.</p>
</article></main></body></html>"""


@dataclass
class FakeBrowserbase:
    """Browserbase's REST API. ``require_project`` makes session creation
    demand a ``projectId`` (as some keys do); ``reject_key`` answers 401."""

    key: str = E2E_KEY
    require_project: bool = False
    reject_key: bool = False
    project_id: str = "proj_e2e"
    created: list[dict] = field(default_factory=list)
    released: list[str] = field(default_factory=list)
    requests: list[httpx.Request] = field(default_factory=list)

    @property
    def open_sessions(self) -> list[str]:
        return [s["id"] for s in self.created if s["id"] not in self.released]

    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._handle)

    def _handle(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        if request.headers.get("x-bb-api-key") != self.key or self.reject_key:
            return httpx.Response(401, json={"error": "Unauthorized"})
        path = request.url.path
        body = json.loads(request.content or b"{}") if request.content else {}
        if request.method == "GET" and path == "/v1/projects":
            return httpx.Response(200, json=[{"id": self.project_id, "name": "e2e"}])
        if request.method == "POST" and path == "/v1/sessions":
            if self.require_project and "projectId" not in body:
                return httpx.Response(400, json={"error": "projectId is required"})
            session_id = f"sess_{uuid.uuid4().hex[:10]}"
            self.created.append({"id": session_id, "body": body})
            return httpx.Response(
                201,
                json={
                    "id": session_id,
                    "projectId": body.get("projectId", self.project_id),
                    "status": "RUNNING",
                    "connectUrl": f"wss://connect.fake-browserbase.test?session={session_id}",
                },
            )
        if request.method == "POST" and path.startswith("/v1/sessions/"):
            session_id = path.rsplit("/", 1)[-1]
            if body.get("status") == "REQUEST_RELEASE":
                self.released.append(session_id)
                return httpx.Response(
                    200, json={"id": session_id, "status": "COMPLETED"}
                )
        return httpx.Response(404, json={"error": "not found"})


@dataclass
class FakeBrowser:
    """The browser behind a session: ``pages`` maps URL -> (title, html)."""

    pages: dict[str, tuple[str, str]] = field(default_factory=dict)
    hang: bool = False
    redirect_to: str | None = None
    loaded: list[tuple[str, str]] = field(default_factory=list)

    async def __call__(self, connect_url: str, url: str):
        from app.notebooks.research.page_read.browser import (
            LoadedPage,
            PageLoadError,
            PageRefusedError,
        )
        from app.utils.crawl import is_publicly_routable

        self.loaded.append((connect_url, url))
        if self.hang:
            await asyncio.sleep(3600)
        if self.redirect_to is not None:
            if not is_publicly_routable(self.redirect_to):
                raise PageRefusedError("the page led to a non-public address")
            url = self.redirect_to
        if url not in self.pages:
            raise PageLoadError("net::ERR_NAME_NOT_RESOLVED")
        title, html = self.pages[url]
        return LoadedPage(url=url, title=title, html=html)


def install(active_patches: list) -> None:
    """E2E worker/backend: Browserbase is 'connected' everywhere and faked."""
    from unittest.mock import patch

    from app.notebooks.research.page_read import browserbase_api

    fake = FakeBrowserbase()
    browser = FakeBrowser(
        pages={E2E_JS_PAGE_URL: (E2E_JS_PAGE_TITLE, E2E_JS_PAGE_HTML)}
    )
    real_init = browserbase_api.BrowserbaseApi.__init__

    def init(self, api_key, *, base_url=None, transport=None):
        real_init(
            self,
            api_key,
            base_url="https://api.fake-browserbase.test",
            transport=fake.transport(),
        )

    async def key(_session, _workspace_id):
        return E2E_KEY

    for target, value in (
        (
            "app.notebooks.research.page_read.browserbase_api.BrowserbaseApi.__init__",
            init,
        ),
        ("app.notebooks.research.page_read.reader.load_over_cdp", browser),
        ("app.notebooks.research.page_read.reader.browserbase_key", key),
        ("app.notebooks.research.routes.browserbase_key", key),
    ):
        p = patch(target, value)
        p.start()
        active_patches.append(p)
