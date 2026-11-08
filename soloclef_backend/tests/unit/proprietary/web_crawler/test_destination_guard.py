"""A crawl stays on public addresses after its first URL: redirects and script navigations.

``crawl_url`` checked only the URL it was given. A page that redirected, or
whose script moved the browser, to a private address was fetched there by a
browser tier, and a static fetch whose redirect curl refused was handed to
the browser tiers, which followed it. The fakes stand in for Scrapling's
fetchers and Playwright's page; ``net_guard`` judges the addresses.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.proprietary.web_crawler import (
    CrawlOutcomeStatus,
    WebCrawlerConnector,
    connector as connector_module,
)
from app.proprietary.web_crawler.destination_guard import (
    BrowserDestinationGuard,
    RestrictedDestinationError,
    ensure_public,
    is_refused_redirect,
)
from app.utils.crawl import net_guard

pytestmark = pytest.mark.unit

PUBLIC = "https://example.com/page"
METADATA = "http://169.254.169.254/latest/meta-data/"
INTERNAL = "http://10.0.0.5/admin"
CURL_REFUSAL = (
    "Failed to perform, curl: (7) Redirect to internal IP 10.0.0.5 rejected "
    "(SSRF protection). See https://curl.se/libcurl/c/libcurl-errors.html"
)
HTML = "<html><body><p>" + "Plenty of readable words here. " * 40 + "</p></body></html>"


@pytest.fixture(autouse=True)
def _public_dns(monkeypatch):
    """Names resolve to a public address; address literals are judged as they are."""
    looked_up: list[str] = []

    def resolve(host):
        looked_up.append(host)
        return ["93.184.216.34"]

    monkeypatch.setattr(net_guard, "_resolve_host", resolve)
    return looked_up


# --- the browser guard on its own -------------------------------------------


class FakeRequest:
    def __init__(self, url, *, navigation=False, main_frame=True):
        self.url = url
        self._navigation = navigation
        self.frame = SimpleNamespace(parent_frame=None if main_frame else object())

    def is_navigation_request(self):
        return self._navigation


class FakeRoute:
    def __init__(self, request):
        self.request = request
        self.outcome = None

    def fallback(self):
        self.outcome = "fallback"

    def abort(self, reason):
        self.outcome = f"abort:{reason}"


class FakeServiceWorkerRequest:
    def __init__(self, url):
        self.url = url

    def is_navigation_request(self):
        return False

    @property
    def frame(self):
        # Playwright raises: a service worker's request belongs to no frame.
        raise RuntimeError("Service Worker requests do not have an associated frame.")


class FakeContext:
    """The browser context: its route is the only one a service worker's requests reach."""

    def __init__(self):
        self.handler = None

    def route(self, pattern, handler):
        assert pattern == "**/*"
        self.handler = handler

    def service_worker_fetch(self, url):
        route = FakeRoute(FakeServiceWorkerRequest(url))
        self.handler(route)
        return route.outcome


class FakeWebSocketRoute:
    def __init__(self, url):
        self.url = url
        self.outcome = None

    def connect_to_server(self):
        self.outcome = "connected"

    def close(self, **_):
        self.outcome = "closed"


class FakePage:
    """Records the guard's route and listener; ``visit`` plays a request through them."""

    def __init__(self):
        self.handler = None
        self.listeners = {}
        self.web_socket_handler = None
        self.context = FakeContext()

    def route(self, pattern, handler):
        assert pattern == "**/*"
        self.handler = handler

    def route_web_socket(self, pattern, handler):
        assert pattern.pattern == ".*"
        self.web_socket_handler = handler

    def open_web_socket(self, url):
        ws = FakeWebSocketRoute(url)
        self.web_socket_handler(ws)
        return ws.outcome

    def on(self, event, listener):
        self.listeners[event] = listener

    def visit(self, url, *, navigation=True, main_frame=True):
        route = FakeRoute(
            FakeRequest(url, navigation=navigation, main_frame=main_frame)
        )
        self.handler(route)
        if route.outcome == "fallback" and navigation and main_frame:
            self.listeners["framenavigated"](
                SimpleNamespace(url=url, parent_frame=None)
            )
        return route.outcome

    def redirected_to(self, url):
        """An HTTP redirect: Chromium follows it without asking the route."""
        self.listeners["framenavigated"](SimpleNamespace(url=url, parent_frame=None))


def test_requests_to_public_hosts_go_on_and_private_ones_are_aborted():
    guard = BrowserDestinationGuard()
    page = FakePage()
    guard.setup(page)
    assert page.visit(PUBLIC) == "fallback"
    assert page.visit("https://cdn.example.org/app.js", navigation=False) == "fallback"
    # A subresource and an iframe to private hosts: aborted, the page itself fine.
    assert page.visit(METADATA, navigation=False) == "abort:blockedbyclient"
    assert page.visit(INTERNAL, main_frame=False) == "abort:blockedbyclient"
    assert page.visit("data:text/plain,hi", navigation=False) == "fallback"
    guard.check(PUBLIC)


def test_a_service_workers_request_to_a_private_host_is_aborted():
    # page.route never sees it; the context's route does.
    guard = BrowserDestinationGuard()
    page = FakePage()
    guard.setup(page)
    assert page.context.service_worker_fetch(PUBLIC) == "fallback"
    assert page.context.service_worker_fetch(METADATA) == "abort:blockedbyclient"
    # Not the page's navigation: the page itself is not refused for it.
    guard.check(PUBLIC)


def test_a_web_socket_to_a_private_host_is_closed_when_asked():
    guard = BrowserDestinationGuard(close_private_web_sockets=True)
    page = FakePage()
    guard.setup(page)
    assert page.open_web_socket("wss://example.com/socket") == "connected"
    assert page.open_web_socket("ws://10.0.0.5:4848/") == "closed"
    assert page.open_web_socket("ws://169.254.169.254/") == "closed"
    # The stealth tier leaves the page's WebSocket alone (a bot check sees
    # Playwright's replacement); its sockets meet the pinned proxy instead.
    plain = FakePage()
    BrowserDestinationGuard().setup(plain)
    assert plain.web_socket_handler is None


def test_a_script_moving_the_page_to_a_private_host_refuses_the_page():
    guard = BrowserDestinationGuard()
    page = FakePage()
    guard.setup(page)
    page.visit(PUBLIC)
    assert page.visit(INTERNAL) == "abort:blockedbyclient"
    with pytest.raises(RestrictedDestinationError):
        guard.check(PUBLIC)


def test_a_redirect_the_route_never_saw_is_refused_after_the_load():
    guard = BrowserDestinationGuard()
    page = FakePage()
    guard.setup(page)
    page.visit(PUBLIC)
    page.redirected_to(METADATA)
    with pytest.raises(RestrictedDestinationError):
        guard.check(METADATA)


def test_each_host_is_resolved_once_per_fetch(_public_dns):
    guard = BrowserDestinationGuard()
    page = FakePage()
    guard.setup(page)
    for path in ("a", "b", "c"):
        page.visit(f"https://cdn.example.org/{path}.js", navigation=False)
    assert _public_dns.count("cdn.example.org") == 1


def test_curls_refusal_and_where_a_static_fetch_ended_are_recognised():
    assert is_refused_redirect(ConnectionError(CURL_REFUSAL))
    assert not is_refused_redirect(ConnectionError("curl: (28) Operation timed out"))
    ensure_public(PUBLIC)
    with pytest.raises(RestrictedDestinationError):
        ensure_public(INTERNAL)


# --- crawl_url: a refusal ends the ladder --------------------------------------


def _browser_tiers_recorded(monkeypatch, crawler):
    called: list[str] = []

    async def dynamic(_url, *_args):
        called.append("dynamic")
        return {"content": "browser content", "metadata": {}, "crawler_type": "x"}

    async def stealthy(_url, *_args):
        called.append("stealthy")
        return {"content": "browser content", "metadata": {}, "crawler_type": "x"}

    monkeypatch.setattr(crawler, "_crawl_with_dynamic", dynamic)
    monkeypatch.setattr(crawler, "_crawl_with_stealthy", stealthy)
    return called


async def test_a_redirect_curl_refused_is_never_tried_in_a_browser(monkeypatch):
    crawler = WebCrawlerConnector()
    called = _browser_tiers_recorded(monkeypatch, crawler)

    async def refused_get(*_args, **_kwargs):
        raise ConnectionError(CURL_REFUSAL)

    monkeypatch.setattr(connector_module.AsyncFetcher, "get", refused_get)
    outcome = await crawler.crawl_url(PUBLIC)
    assert outcome.status is CrawlOutcomeStatus.FAILED
    assert "Restricted URL" in (outcome.error or "")
    assert called == []


async def test_a_static_fetch_that_ended_on_a_private_host_is_refused(monkeypatch):
    crawler = WebCrawlerConnector()
    called = _browser_tiers_recorded(monkeypatch, crawler)

    async def landed_inside(*_args, **_kwargs):
        return SimpleNamespace(status=200, url=INTERNAL, html_content=HTML)

    monkeypatch.setattr(connector_module.AsyncFetcher, "get", landed_inside)
    outcome = await crawler.crawl_url(PUBLIC)
    assert outcome.status is CrawlOutcomeStatus.FAILED
    assert "Restricted URL" in (outcome.error or "")
    assert outcome.result is None
    assert called == []


class FakeDynamicFetcher:
    """Plays one page load through the page_setup the tier passes."""

    def __init__(self, script):
        self.script = script
        self.calls = 0

    def fetch(self, url, **kwargs):
        self.calls += 1
        page = FakePage()
        kwargs["page_setup"](page)
        return self.script(page, url)


def _static_misses(monkeypatch, crawler):
    async def miss(_url, *_args):
        return None

    monkeypatch.setattr(crawler, "_crawl_with_async_fetcher", miss)


async def test_a_page_redirected_inside_by_the_browser_is_refused(monkeypatch):
    crawler = WebCrawlerConnector()
    _static_misses(monkeypatch, crawler)

    def redirected(page, url):
        page.visit(url)
        page.redirected_to(METADATA)
        return SimpleNamespace(status=200, url=METADATA, html_content=HTML)

    fetcher = FakeDynamicFetcher(redirected)
    monkeypatch.setattr(connector_module, "DynamicFetcher", fetcher)
    stealthy_calls: list[str] = []

    async def stealthy(_url, *_args):
        stealthy_calls.append("stealthy")
        return {"content": "x", "metadata": {}, "crawler_type": "x"}

    monkeypatch.setattr(crawler, "_crawl_with_stealthy", stealthy)
    outcome = await crawler.crawl_url(PUBLIC)
    assert outcome.status is CrawlOutcomeStatus.FAILED
    assert outcome.tier == "scrapling-dynamic"
    assert "Restricted URL" in (outcome.error or "")
    assert outcome.result is None
    assert stealthy_calls == []


async def test_a_script_navigation_inside_is_stopped_and_the_crawl_ends(monkeypatch):
    crawler = WebCrawlerConnector()
    _static_misses(monkeypatch, crawler)

    def moved_by_script(page, url):
        page.visit(url)
        if page.visit(INTERNAL) != "fallback":
            raise RuntimeError("net::ERR_BLOCKED_BY_CLIENT")
        return SimpleNamespace(status=200, url=INTERNAL, html_content=HTML)

    monkeypatch.setattr(
        connector_module, "DynamicFetcher", FakeDynamicFetcher(moved_by_script)
    )
    stealthy_calls: list[str] = []

    async def stealthy(_url, *_args):
        stealthy_calls.append("stealthy")
        return {"content": "x", "metadata": {}, "crawler_type": "x"}

    monkeypatch.setattr(crawler, "_crawl_with_stealthy", stealthy)
    outcome = await crawler.crawl_url(PUBLIC)
    assert outcome.status is CrawlOutcomeStatus.FAILED
    assert "Restricted URL" in (outcome.error or "")
    assert stealthy_calls == []


async def test_a_public_page_still_crawls_in_the_browser(monkeypatch):
    crawler = WebCrawlerConnector()
    _static_misses(monkeypatch, crawler)

    def ordinary(page, url):
        page.visit(url)
        page.visit("https://cdn.example.org/app.js", navigation=False)
        return SimpleNamespace(status=200, url=url, html_content=HTML)

    monkeypatch.setattr(
        connector_module, "DynamicFetcher", FakeDynamicFetcher(ordinary)
    )
    outcome = await crawler.crawl_url(PUBLIC)
    assert outcome.status is CrawlOutcomeStatus.SUCCESS
    assert outcome.tier == "scrapling-dynamic"
