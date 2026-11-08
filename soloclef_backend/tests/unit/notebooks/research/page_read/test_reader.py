"""Read a pasted page: Browserbase first, the crawler as the fallback."""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.notebooks.research.ingest.fetched import FetchedContent
from app.notebooks.research.page_read import reader
from app.notebooks.research.page_read.browserbase_api import BrowserbaseApi
from app.notebooks.research.page_read.public_http import NotPublicError
from app.notebooks.research.page_read.reader import PageNotPublicError, read_web_page
from tests.e2e.fakes.browserbase import (
    E2E_JS_PAGE_HTML,
    E2E_JS_PAGE_TEXT,
    E2E_JS_PAGE_TITLE,
    E2E_KEY,
    FakeBrowser,
    FakeBrowserbase,
)

pytestmark = pytest.mark.unit

URL = "https://93.184.215.14/e2e-js-page"


class _Runner:
    workspace_id = 7

    def __init__(self):
        self.charged: list[int] = []

    async def charge_pages(self, pages: int) -> int:
        self.charged.append(pages)
        return 0


@pytest.fixture
def world():
    state = SimpleNamespace(
        bb=FakeBrowserbase(),
        browser=FakeBrowser(pages={URL: (E2E_JS_PAGE_TITLE, E2E_JS_PAGE_HTML)}),
        key=E2E_KEY,
        crawled=[],
        walked=[],
        crawl_result=FetchedContent(markdown="crawled text", title="Crawled"),
        runner=_Runner(),
    )

    async def key_lookup(_session, workspace_id):
        assert workspace_id == 7
        return state.key

    async def crawl(_session, _runner, url, _user):
        state.crawled.append(url)
        return state.crawl_result

    async def walk(url):
        state.walked.append(url)
        return url + "?landed"

    async def read(url=URL):
        return await read_web_page(
            None,
            state.runner,
            url,
            "user",
            key_lookup=key_lookup,
            api_factory=lambda key: BrowserbaseApi(
                key, base_url="https://bb.test", transport=state.bb.transport()
            ),
            loader=state.browser,
            crawl=crawl,
            walk_redirects=walk,
        )

    state.read = read
    return state


async def test_browserbase_renders_the_page_and_the_session_is_ended(world):
    content = await world.read()
    assert E2E_JS_PAGE_TEXT in content.markdown
    assert content.title == E2E_JS_PAGE_TITLE
    assert content.metadata["reader"] == "browserbase"
    assert world.bb.open_sessions == [] and len(world.bb.created) == 1
    assert world.runner.charged == [1]
    assert world.crawled == []


async def test_a_page_that_never_loads_ends_the_session_and_falls_back(
    world, monkeypatch
):
    monkeypatch.setattr(reader, "BROWSERBASE_BUDGET_S", 0.05)
    world.browser.hang = True
    # read_with_browserbase takes the budget as a default argument.
    real = reader.read_with_browserbase

    async def quick(url, api, *, loader=None, budget_s=0.05):
        return await real(url, api, loader=loader, budget_s=budget_s)

    monkeypatch.setattr(reader, "read_with_browserbase", quick)
    content = await world.read()
    assert content.markdown == "crawled text"
    assert content.metadata["reader"] == "crawler"
    assert world.bb.open_sessions == []
    assert world.crawled == [URL + "?landed"]
    assert world.runner.charged == []


async def test_browserbase_refusing_the_key_falls_back_to_the_crawler(world):
    world.bb.reject_key = True
    content = await world.read()
    assert content.metadata["reader"] == "crawler"


async def test_without_browserbase_the_crawler_reads_it(world):
    world.key = None
    content = await world.read()
    assert content.metadata["reader"] == "crawler"
    assert world.bb.requests == []


async def test_a_redirect_to_a_private_address_is_refused_not_crawled(world):
    world.browser.redirect_to = "http://169.254.169.254/latest/meta-data/"
    with pytest.raises(PageNotPublicError):
        await world.read()
    assert world.crawled == []
    assert world.bb.open_sessions == []


async def test_a_private_url_is_refused_before_anything_runs(world):
    with pytest.raises(PageNotPublicError):
        await world.read("http://127.0.0.1:8000/admin")
    assert world.bb.requests == [] and world.crawled == []


async def test_the_crawler_fallback_refuses_a_private_redirect(world, monkeypatch):
    world.key = None

    async def private_walk(url):
        raise NotPublicError("10.0.0.5 is not a public address")

    with pytest.raises(PageNotPublicError):
        await read_web_page(
            None,
            world.runner,
            URL,
            "user",
            key_lookup=lambda *_: _none(),
            walk_redirects=private_walk,
            crawl=lambda *_: _none(),
        )


async def _none():
    return None
