"""The pinned egress: a fetch connects only to the addresses that were checked.

The checks resolved a URL's host and the fetcher resolved it again, so a
name answering a public address first and a private one next (DNS
rebinding) read this server's internal pages; Chromium's redirect hops,
WebSockets and service workers never met the checks at all. The proxy here
resolves once per connection and connects to what it judged. Loopback
stands in for the internet: 127.0.0.1 is "public", 127.0.0.2 "private".
"""

from __future__ import annotations

import asyncio
import http.server
import socket
import socketserver
import struct
import threading

import pytest

from app.proprietary.web_crawler import connector as connector_module, pinned_egress
from app.proprietary.web_crawler.destination_guard import RestrictedDestinationError
from app.proprietary.web_crawler.pinned_egress import PinnedEgress, egress_for

pytestmark = pytest.mark.unit

PUBLIC_IP, PRIVATE_IP = "127.0.0.1", "127.0.0.2"


def is_public(address: str) -> bool:
    return address == PUBLIC_IP


class _Site(http.server.BaseHTTPRequestHandler):
    body = b"PUBLIC"
    hits: list[str]

    def log_message(self, *args):
        pass

    def do_GET(self):
        self.hits.append(self.path)
        if self.path == "/to-internal":
            self.send_response(302)
            self.send_header("Location", f"http://internal.test:{self.server.other}/")
            self.end_headers()
            return
        if self.path == "/to-public":
            self.send_response(302)
            self.send_header("Location", "/")
            self.end_headers()
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(self.body)))
        self.end_headers()
        self.wfile.write(self.body)


def _serve(address: str, body: bytes):
    hits: list[str] = []
    handler = type("Site", (_Site,), {"body": body, "hits": hits})
    server = socketserver.ThreadingTCPServer((address, 0), handler)
    server.daemon_threads = True
    threading.Thread(
        target=server.serve_forever, kwargs={"poll_interval": 0.02}, daemon=True
    ).start()
    return server, hits


@pytest.fixture
def sites():
    public, public_hits = _serve(PUBLIC_IP, b"PUBLIC")
    private, private_hits = _serve(PRIVATE_IP, b"SECRET")
    public.other = private.server_address[1]
    yield public.server_address[1], private.server_address[1], public_hits, private_hits
    public.shutdown()
    private.shutdown()


def _names(answers: dict[str, list[list[str]]]):
    """A resolver whose answers for a name change at each lookup (the rebinding)."""
    asked: list[str] = []

    async def resolve(host, port):
        asked.append(host)
        queue = answers.get(host, [[]])
        return queue.pop(0) if len(queue) > 1 else queue[0]

    return resolve, asked


def _socks_connect(port: int, host: str, target_port: int) -> tuple[int, socket.socket]:
    """A SOCKS5 CONNECT by host name, as Chromium and curl's socks5h send it."""
    sock = socket.create_connection(("127.0.0.1", port), timeout=5)
    sock.sendall(b"\x05\x01\x00")
    assert sock.recv(2) == b"\x05\x00"
    name = host.encode()
    sock.sendall(
        b"\x05\x01\x00\x03" + bytes([len(name)]) + name + struct.pack("!H", target_port)
    )
    reply = sock.recv(10)
    return reply[1], sock


def _get(sock: socket.socket, host: str) -> bytes:
    sock.sendall(f"GET / HTTP/1.0\r\nHost: {host}\r\n\r\n".encode())
    data = b""
    while chunk := sock.recv(4096):
        data += chunk
    return data


def test_a_public_name_is_connected_to_the_address_it_was_checked_at(sites):
    public_port, _, hits, private_hits = sites
    resolve, _ = _names({"site.test": [[PUBLIC_IP]]})
    with PinnedEgress(resolve=resolve, is_public=is_public) as egress:
        code, sock = _socks_connect(egress.port, "site.test", public_port)
        assert code == 0
        assert _get(sock, "site.test").endswith(b"PUBLIC")
        sock.close()
        assert egress.refused == 0
    assert hits == ["/"] and private_hits == []


def test_a_name_that_rebinds_to_a_private_address_is_refused(sites):
    # The guard's lookup said public; the next lookup (the one a fetcher's
    # own resolver would have used) says private.
    _, private_port, _, private_hits = sites
    resolve, asked = _names({"rebind.test": [[PRIVATE_IP]]})
    with PinnedEgress(resolve=resolve, is_public=is_public) as egress:
        code, sock = _socks_connect(egress.port, "rebind.test", private_port)
        sock.close()
        assert code == 2  # connection not allowed by ruleset
        assert egress.refused == 1
    assert asked == ["rebind.test"]
    assert private_hits == []


def test_one_private_answer_among_public_ones_refuses_the_name(sites):
    public_port, _, hits, _ = sites
    resolve, _ = _names({"mixed.test": [[PUBLIC_IP, PRIVATE_IP]]})
    with PinnedEgress(resolve=resolve, is_public=is_public) as egress:
        code, sock = _socks_connect(egress.port, "mixed.test", public_port)
        sock.close()
        assert code == 2
    assert hits == []


def test_an_address_literal_is_judged_without_a_lookup(sites):
    _, private_port, _, private_hits = sites
    resolve, asked = _names({})
    with PinnedEgress(resolve=resolve, is_public=is_public) as egress:
        code, sock = _socks_connect(egress.port, PRIVATE_IP, private_port)
        sock.close()
        assert code == 2
        assert egress.refused == 1
    assert asked == [] and private_hits == []


def test_a_name_that_does_not_resolve_is_refused():
    async def fails(host, port):
        raise OSError("no such host")

    with PinnedEgress(resolve=fails, is_public=is_public) as egress:
        code, sock = _socks_connect(egress.port, "nowhere.test", 80)
        sock.close()
        assert code == 2


def test_curl_follows_a_public_redirect_and_is_refused_a_private_hop(sites):
    from curl_cffi import requests as curl

    public_port, _, hits, private_hits = sites
    resolve, _ = _names({"site.test": [[PUBLIC_IP]], "internal.test": [[PRIVATE_IP]]})
    with PinnedEgress(resolve=resolve, is_public=is_public) as egress:
        # Every redirect, as the static tier asks through the pinned proxy.
        page = curl.get(
            f"http://site.test:{public_port}/to-public",
            proxy=egress.curl_proxy,
            allow_redirects=True,
            timeout=10,
        )
        assert page.text == "PUBLIC"
        with pytest.raises(Exception) as failed:
            curl.get(
                f"http://site.test:{public_port}/to-internal",
                proxy=egress.curl_proxy,
                allow_redirects=True,
                timeout=10,
            )
        with pytest.raises(RestrictedDestinationError):
            egress.raise_if_refused(failed.value)
    assert private_hits == []
    assert hits == ["/to-public", "/", "/to-internal"]


def test_curls_safe_redirects_cannot_be_used_through_it(sites):
    # Why the static tier asks for every redirect when pinned: "safe" judges
    # the address curl is connected to, the proxy's 127.0.0.1.
    from curl_cffi import requests as curl

    public_port, _, _, _ = sites
    resolve, _ = _names({"site.test": [[PUBLIC_IP]]})
    with (
        PinnedEgress(resolve=resolve, is_public=is_public) as egress,
        pytest.raises(Exception, match="SSRF protection"),
    ):
        curl.get(
            f"http://site.test:{public_port}/to-public",
            proxy=egress.curl_proxy,
            allow_redirects="safe",
            timeout=10,
        )


async def test_it_opens_without_blocking_another_event_loop(sites):
    public_port, _, _, _ = sites
    resolve, _ = _names({"site.test": [[PUBLIC_IP]]})
    async with PinnedEgress(resolve=resolve, is_public=is_public) as egress:
        code, sock = await asyncio.to_thread(
            _socks_connect, egress.port, "site.test", public_port
        )
        sock.close()
        assert code == 0
    # Closed with the fetch: nothing listens any more.
    with pytest.raises(OSError):
        socket.create_connection(("127.0.0.1", egress.port), timeout=1)


def test_the_operators_proxy_takes_its_place():
    with egress_for("http://proxy.example:8888") as egress:
        assert not egress.pinned
        assert egress.browser_proxy is None and egress.curl_proxy is None
        egress.raise_if_refused(RuntimeError("anything"))
    with egress_for(None) as egress:
        assert egress.pinned
        assert egress.browser_proxy == f"socks5://127.0.0.1:{egress.port}"
        assert egress.curl_proxy == f"socks5h://127.0.0.1:{egress.port}"


# --- the tiers ask for it ------------------------------------------------


class _Page:
    status = 200
    url = None
    html_content = (
        "<html><body>" + "<p>Plenty of words here.</p>" * 40 + "</body></html>"
    )


@pytest.mark.parametrize("operator_proxy", [None, "http://proxy.example:8888"])
async def test_every_tier_connects_through_it_unless_the_operator_has_a_proxy(
    monkeypatch, operator_proxy
):
    seen: dict[str, dict] = {}
    monkeypatch.setattr(connector_module, "get_proxy_url", lambda: operator_proxy)

    async def static_get(url, **kwargs):
        seen["static"] = kwargs
        return _Page()

    def dynamic_fetch(url, **kwargs):
        seen["dynamic"] = kwargs
        return _Page()

    def stealthy_fetch(url, **kwargs):
        seen["stealthy"] = kwargs
        return _Page()

    monkeypatch.setattr(connector_module.AsyncFetcher, "get", static_get)
    monkeypatch.setattr(connector_module.DynamicFetcher, "fetch", dynamic_fetch)
    monkeypatch.setattr(connector_module.StealthyFetcher, "fetch", stealthy_fetch)
    crawler = connector_module.WebCrawlerConnector()
    await crawler._crawl_with_async_fetcher("https://example.com/")
    await crawler._crawl_with_dynamic("https://example.com/")
    await crawler._crawl_with_stealthy("https://example.com/")

    if operator_proxy is None:
        assert seen["static"]["proxy"].startswith("socks5h://127.0.0.1:")
        assert seen["static"]["follow_redirects"] is True
        assert seen["dynamic"]["proxy"].startswith("socks5://127.0.0.1:")
        assert seen["stealthy"]["proxy"].startswith("socks5://127.0.0.1:")
    else:
        assert seen["static"]["proxy"] == operator_proxy
        assert seen["static"]["follow_redirects"] == "safe"
        assert seen["dynamic"]["proxy"] == operator_proxy
        assert seen["stealthy"]["proxy"] == operator_proxy


async def test_a_browser_fetch_that_failed_after_a_refusal_ends_the_crawl(
    monkeypatch, sites
):
    _, private_port, _, private_hits = sites
    monkeypatch.setattr(connector_module, "get_proxy_url", lambda: None)
    monkeypatch.setattr(pinned_egress, "is_public_address", is_public)

    async def resolve(host, port):
        return [PRIVATE_IP]

    monkeypatch.setattr(pinned_egress, "_resolve", resolve)

    def chromium(url, **kwargs):
        # What Chromium does through the proxy: the hop is refused there.
        port = int(kwargs["proxy"].rsplit(":", 1)[1])
        code, sock = _socks_connect(port, "rebind.test", private_port)
        sock.close()
        assert code == 2
        raise RuntimeError("net::ERR_SOCKS_CONNECTION_FAILED at http://rebind.test/")

    monkeypatch.setattr(connector_module.DynamicFetcher, "fetch", chromium)
    crawler = connector_module.WebCrawlerConnector()
    with pytest.raises(RestrictedDestinationError):
        await crawler._crawl_with_dynamic("https://rebind.test/")
    assert private_hits == []
