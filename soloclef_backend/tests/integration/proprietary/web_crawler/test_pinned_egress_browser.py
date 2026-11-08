"""The browser tier through the pinned egress, in a real Chromium.

Three ways a page reached this server's internal addresses past the checks:
a name that answered the check a public address and Chromium a private one
(DNS rebinding), an HTTP redirect hop (Chromium follows it without asking
``page.route``), and a WebSocket (never routed). Chromium's own resolver is
pointed at the "private" address with ``--host-resolver-rules``, so without
the pinned proxy each would reach it; the checks' resolver says every name
is public. Loopback stands in: 127.0.0.1 is "public", 127.0.0.2 "private".

Needs a Chromium Scrapling can start: its own, or the one
``CRAWLER_TEST_CHROMIUM`` names (a path to a chrome or headless shell).
Skipped otherwise. No database.
"""

from __future__ import annotations

import http.server
import os
import socketserver
import threading

import pytest

from app.proprietary.web_crawler import connector as connector_module, pinned_egress
from app.proprietary.web_crawler.destination_guard import RestrictedDestinationError
from app.utils.crawl import net_guard

pytestmark = pytest.mark.integration

PUBLIC_IP, PRIVATE_IP = "127.0.0.1", "127.0.0.2"
WORDS = "<p>" + "A page with plenty of readable words in it. " * 40 + "</p>"


def _site(body_for, hits):
    class Site(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            hits.append(self.path)
            status, headers, body = body_for(self.path)
            self.send_response(status)
            for name, value in headers.items():
                self.send_header(name, value)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    return Site


def _serve(address, handler):
    server = socketserver.ThreadingTCPServer((address, 0), handler)
    server.daemon_threads = True
    threading.Thread(
        target=server.serve_forever, kwargs={"poll_interval": 0.02}, daemon=True
    ).start()
    return server


@pytest.fixture
def network(monkeypatch):
    private_hits: list[str] = []
    private = _serve(
        PRIVATE_IP,
        _site(
            lambda path: (200, {"Content-Type": "text/html"}, b"SECRET " * 200),
            private_hits,
        ),
    )
    secret_port = private.server_address[1]

    def public_pages(path):
        if path == "/to-internal":
            return 302, {"Location": f"http://internal.test:{secret_port}/"}, b""
        if path == "/ws-page":
            page = (
                f"<html><body>{WORDS}<p id='s'>start</p><script>"
                f"const w = new WebSocket('ws://internal.test:{secret_port}/');"
                "w.onopen = () => document.getElementById('s').textContent = 'OPEN';"
                "w.onerror = () => document.getElementById('s').textContent = 'REFUSED';"
                "</script></body></html>"
            )
            return 200, {"Content-Type": "text/html"}, page.encode()
        return (
            200,
            {"Content-Type": "text/html"},
            f"<html><body>{WORDS}</body></html>".encode(),
        )

    public_hits: list[str] = []
    public = _serve(PUBLIC_IP, _site(public_pages, public_hits))

    # The checks: every name is public (a rebinding name's first answer).
    monkeypatch.setattr(net_guard, "_resolve_host", lambda host: ["93.184.216.34"])
    # The pinned proxy's own lookup: the names' next answers.
    answers = {
        "site.test": [PUBLIC_IP],
        "internal.test": [PRIVATE_IP],
        "rebind.test": [PRIVATE_IP],
    }

    async def resolve(host, port):
        return answers.get(host, [])

    monkeypatch.setattr(pinned_egress, "_resolve", resolve)
    monkeypatch.setattr(pinned_egress, "is_public_address", lambda a: a == PUBLIC_IP)
    monkeypatch.setattr(connector_module, "get_proxy_url", lambda: None)

    # Chromium's own resolver sends each name to its "real" address.
    rules = (
        f"MAP site.test {PUBLIC_IP}, MAP internal.test {PRIVATE_IP}, "
        f"MAP rebind.test {PRIVATE_IP}"
    )
    original = connector_module.DynamicFetcher.fetch
    executable = os.environ.get("CRAWLER_TEST_CHROMIUM")

    def fetch(url, **kwargs):
        kwargs["extra_flags"] = [f"--host-resolver-rules={rules}"]
        if executable:
            kwargs["executable_path"] = executable
        return original(url, **kwargs)

    monkeypatch.setattr(connector_module.DynamicFetcher, "fetch", fetch)

    yield public.server_address[1], secret_port, public_hits, private_hits
    public.shutdown()
    private.shutdown()


def _crawl(url):
    crawler = connector_module.WebCrawlerConnector()
    try:
        return crawler._crawl_with_dynamic_sync(url)
    except RestrictedDestinationError:
        raise
    except Exception as exc:
        if "Executable doesn't exist" in str(exc) or "playwright install" in str(exc):
            pytest.skip("no Chromium for Scrapling here (set CRAWLER_TEST_CHROMIUM)")
        raise


def test_a_public_page_still_crawls(network):
    public_port, _, public_hits, _ = network
    result = _crawl(f"http://site.test:{public_port}/")
    assert result is not None and "plenty of readable words" in result["content"]
    assert "/" in public_hits


def test_a_name_rebound_to_a_private_address_is_never_read(network):
    _, secret_port, _, private_hits = network
    with pytest.raises(RestrictedDestinationError):
        _crawl(f"http://rebind.test:{secret_port}/")
    assert private_hits == []


def test_a_redirect_hop_to_a_private_host_is_never_requested(network):
    public_port, _, public_hits, private_hits = network
    with pytest.raises(RestrictedDestinationError):
        _crawl(f"http://site.test:{public_port}/to-internal")
    assert "/to-internal" in public_hits
    assert private_hits == []


def test_a_web_socket_to_a_private_host_never_opens(network):
    public_port, _, _, private_hits = network
    result = _crawl(f"http://site.test:{public_port}/ws-page")
    assert result is not None
    assert private_hits == []
