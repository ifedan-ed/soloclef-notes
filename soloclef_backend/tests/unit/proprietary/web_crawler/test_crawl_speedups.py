"""Faster crawls without hidden cuts: dead pages stop early, start URLs run
side by side within bounds, and every limit that cuts a crawl is stated."""

from __future__ import annotations

import asyncio
import threading
import time

import pytest

from app.capabilities.core.access.agent import _build_preview, _notices_header
from app.capabilities.core.runs import RUN_OUTPUT_CHAR_CAP, serialize_output
from app.capabilities.web.crawl.executor import build_crawl_executor
from app.capabilities.web.crawl.schemas import CrawlInput, CrawlItem, CrawlOutput
from app.proprietary.web_crawler import (
    CrawlOutcome,
    CrawlOutcomeStatus,
    WebCrawlerConnector,
    connector as connector_module,
)
from app.proprietary.web_crawler.browser_slots import browser_slot
from app.proprietary.web_crawler.not_found import is_definitely_not_found
from app.proprietary.web_crawler.site_crawler import crawl_site
from app.proprietary.web_crawler.start_urls import fetch_start_urls
from app.utils.crawl import net_guard

pytestmark = pytest.mark.unit

_SUCCESS = CrawlOutcomeStatus.SUCCESS

_SERVER_404 = (
    "<html><head><title>Page not found</title><script src='/theme.js'></script>"
    "</head><body><nav>Home Blog Pricing About Contact Careers Press</nav>"
    "<h1>Oops! That page can't be found.</h1><p>It looks like nothing was found "
    "at this location. Maybe try one of the links below or a search? Recent "
    "posts: Disability insurance for physicians, How riders work, Own-occupation "
    "definitions explained, Choosing a benefit amount, Residents and fellows."
    "</p></body></html>"
)
_SPA_SHELL_404 = (
    "<html><head><script src='/app.js'></script></head>"
    "<body><div id='root'></div><noscript>Enable JavaScript</noscript></body></html>"
)


@pytest.fixture(autouse=True)
def _public_destination(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(net_guard, "_resolve_host", lambda _host: ["93.184.216.34"])


def _static_page(monkeypatch: pytest.MonkeyPatch, status: int, html: str) -> None:
    class _Page:
        pass

    page = _Page()
    page.status = status
    page.html_content = html

    class _AsyncFetcher:
        @staticmethod
        async def get(*_args, **_kwargs):
            return page

    monkeypatch.setattr(connector_module, "AsyncFetcher", _AsyncFetcher)
    monkeypatch.setattr(connector_module, "get_proxy_url", lambda: None)


# --- dead pages ---------------------------------------------------------------


async def test_a_real_404_page_stops_without_a_browser(monkeypatch):
    _static_page(monkeypatch, 404, _SERVER_404)
    crawler = WebCrawlerConnector()
    browser_calls: list[str] = []

    async def _browser(_url, *_args):
        browser_calls.append("browser")
        return None

    monkeypatch.setattr(crawler, "_crawl_with_dynamic", _browser)
    monkeypatch.setattr(crawler, "_crawl_with_stealthy", _browser)

    outcome = await crawler.crawl_url("https://patternlife.test/blog/average-cost")

    assert outcome.status is CrawlOutcomeStatus.FAILED  # not billed
    assert "Page not found (HTTP 404)" in (outcome.error or "")
    assert browser_calls == []


async def test_a_404_app_shell_still_gets_the_browser(monkeypatch):
    _static_page(monkeypatch, 404, _SPA_SHELL_404)
    crawler = WebCrawlerConnector()

    async def _dynamic(_url, *_args):
        return {"content": "rendered route", "metadata": {}, "crawler_type": "d"}

    monkeypatch.setattr(crawler, "_crawl_with_dynamic", _dynamic)

    outcome = await crawler.crawl_url("https://spa.test/route")

    assert outcome.status is CrawlOutcomeStatus.SUCCESS
    assert outcome.tier == "scrapling-dynamic"


def test_only_404_and_410_count_as_gone():
    assert is_definitely_not_found(404, _SERVER_404)
    assert is_definitely_not_found(410, "<h1>Gone</h1>")
    assert is_definitely_not_found(404, "")  # nothing to render either
    assert not is_definitely_not_found(404, _SPA_SHELL_404)
    assert not is_definitely_not_found(403, _SERVER_404)  # blocked, not gone
    assert not is_definitely_not_found(200, _SERVER_404)


# --- start URLs side by side ----------------------------------------------------


class _SlowEngine:
    """Records how many fetches overlap, overall and per host."""

    def __init__(self, delay: float = 0.02):
        self.delay = delay
        self.calls: list[str] = []
        self.active = 0
        self.peak = 0
        self.active_by_host: dict[str, int] = {}
        self.peak_by_host: dict[str, int] = {}

    async def crawl_url(self, url: str) -> CrawlOutcome:
        host = url.split("/")[2]
        self.calls.append(url)
        self.active += 1
        self.active_by_host[host] = self.active_by_host.get(host, 0) + 1
        self.peak = max(self.peak, self.active)
        self.peak_by_host[host] = max(
            self.peak_by_host.get(host, 0), self.active_by_host[host]
        )
        await asyncio.sleep(self.delay)
        self.active -= 1
        self.active_by_host[host] -= 1
        return CrawlOutcome(
            status=_SUCCESS,
            result={"content": f"C:{url}", "metadata": {}, "links": []},
        )


async def test_start_urls_run_side_by_side_within_bounds_and_keep_their_order():
    urls = [f"https://a.test/{i}" for i in range(5)] + [
        f"https://b.test/{i}" for i in range(3)
    ]
    engine = _SlowEngine()

    started = time.perf_counter()
    fetched = await fetch_start_urls(
        engine, urls, concurrency=4, per_host_concurrency=2
    )
    elapsed = time.perf_counter() - started

    assert [url for url, _ in fetched] == urls
    assert engine.peak == 4
    assert engine.peak_by_host == {"a.test": 2, "b.test": 2}
    assert elapsed < len(urls) * engine.delay  # faster than one after another


async def test_depth_zero_fetches_every_start_url_once_even_past_the_page_cap():
    urls = [f"https://e.test/{i}" for i in range(12)] + ["https://e.test/0#top"]
    engine = _SlowEngine(delay=0)

    pages = await crawl_site(engine, urls, max_crawl_depth=0, max_crawl_pages=10)

    assert [page.url for page in pages] == urls[:12]
    assert sorted(engine.calls) == sorted(urls[:12])


def test_browser_fetches_wait_for_a_free_slot():
    peak = 0
    active = 0
    lock = threading.Lock()

    def fetch():
        nonlocal peak, active
        with browser_slot():
            with lock:
                active += 1
                peak = max(peak, active)
            time.sleep(0.02)
            with lock:
                active -= 1

    threads = [threading.Thread(target=fetch) for _ in range(8)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    assert 1 <= peak <= 3


# --- limits are stated ------------------------------------------------------------


async def test_the_page_cap_and_cut_pages_are_stated_in_notices():
    class _SiteEngine:
        async def crawl_url(self, url: str) -> CrawlOutcome:
            links = (
                [f"https://e.test/{i}" for i in range(20)] if url.endswith("/") else []
            )
            return CrawlOutcome(
                status=_SUCCESS,
                result={"content": "x" * 120, "metadata": {}, "links": links},
            )

    execute = build_crawl_executor(engine=_SiteEngine())
    out = await execute(
        CrawlInput(
            startUrls=["https://e.test/"],
            maxCrawlDepth=1,
            maxCrawlPages=3,
            maxLength=100,
        )
    )

    assert len(out.items) == 3
    assert all(item.truncated for item in out.items)
    assert any("maxCrawlPages=3" in notice for notice in out.notices)
    assert any("maxLength=100" in notice for notice in out.notices)


async def test_a_short_scrape_has_no_notices_and_no_truncation_flag():
    execute = build_crawl_executor(engine=_SlowEngine(delay=0))
    out = await execute(CrawlInput(startUrls=["https://e.test/a"]))
    assert out.notices == []
    assert out.items[0].truncated is None


def test_notices_survive_into_the_preview_of_a_large_result():
    big = "y" * (RUN_OUTPUT_CHAR_CAP // 2)
    output = CrawlOutput(
        items=[
            CrawlItem(url=f"https://e.test/{i}", status="success", markdown=big)
            for i in range(4)
        ],
        notices=["Stopped after maxCrawlPages=4 pages; the site may have more."],
    )
    serialized = serialize_output(output)
    assert serialized.char_count > RUN_OUTPUT_CHAR_CAP

    content = _notices_header(output) + _build_preview(serialized, "abc")

    assert content.startswith("Notices (limits that cut this result short")
    assert "maxCrawlPages=4" in content
