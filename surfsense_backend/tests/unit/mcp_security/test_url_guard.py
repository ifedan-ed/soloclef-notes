"""MCP destinations: allowlist, public addresses only, pinned connect, no redirects."""

from __future__ import annotations

import httpcore
import pytest

from app.mcp_security import url_guard
from app.mcp_security.url_guard import (
    MCPDestinationRefusedError,
    check_destination,
    check_url_shape,
    guarded_http_client_factory,
)

pytestmark = pytest.mark.unit

ALLOWED = frozenset(
    {"mcp.exa.ai", "mcp.tavily.com", "mcp.browserbase.com", "evil.test"}
)
PUBLIC_IP = "93.184.215.14"


@pytest.fixture
def dns(monkeypatch):
    """Point host names at chosen addresses; each call pops the next answer."""
    answers: dict[str, list[list[str]]] = {}

    async def fake(host: str, port: int) -> list[str]:
        queue = answers.get(host)
        if not queue:
            raise OSError("NXDOMAIN")
        return queue.pop(0) if len(queue) > 1 else queue[0]

    monkeypatch.setattr(url_guard, "_getaddrinfo", fake)
    return answers


@pytest.mark.parametrize(
    "url",
    [
        "http://mcp.exa.ai/mcp",  # not https
        "https://mcp.exa.ai:4000/mcp",  # other port
        "https://user:pw@mcp.exa.ai/mcp",  # credentials
        "https://127.0.0.1/mcp",  # IP literal
        "https://[::1]/mcp",
        "https://169.254.169.254/latest/meta-data/",
        "https://litellm:4000/",  # docker service
        "https://litellm/",
        "https://localhost/mcp",
        "https://example.com/mcp",  # not on the list
        "https://mcp.exa.ai.evil.com/mcp",  # suffix trick
        "file:///etc/passwd",
        "",
    ],
)
def test_url_shape_refusals(url):
    with pytest.raises(MCPDestinationRefusedError):
        check_url_shape(url, ALLOWED)


def test_allowlisted_https_url_passes_shape():
    assert check_url_shape("https://MCP.Exa.AI./mcp?x=1", ALLOWED) == "mcp.exa.ai"


@pytest.mark.parametrize(
    "addresses",
    [
        ["127.0.0.1"],
        ["10.0.0.5"],
        ["172.17.0.3"],  # docker bridge
        ["172.31.255.1"],
        ["192.168.1.10"],
        ["169.254.169.254"],  # link-local / metadata
        ["100.64.0.1"],  # CGNAT
        ["::1"],
        ["fe80::1"],
        ["::ffff:127.0.0.1"],
        [PUBLIC_IP, "10.0.0.5"],  # one private answer is enough to refuse
    ],
)
async def test_allowlisted_name_resolving_private_is_refused(dns, addresses):
    dns["evil.test"] = [addresses]
    with pytest.raises(MCPDestinationRefusedError):
        await check_destination("https://evil.test/mcp", ALLOWED)


async def test_unresolvable_host_is_refused(dns):
    with pytest.raises(MCPDestinationRefusedError):
        await check_destination("https://mcp.exa.ai/mcp", ALLOWED)


async def test_public_allowlisted_host_passes(dns):
    dns["mcp.exa.ai"] = [[PUBLIC_IP]]
    assert await check_destination("https://mcp.exa.ai/mcp", ALLOWED) == "mcp.exa.ai"


class _Recorder:
    def __init__(self):
        self.calls: list[tuple[str, int]] = []

    async def connect_tcp(self, host, port, **kwargs):
        self.calls.append((host, port))
        return object()


def _backend():
    client = guarded_http_client_factory(ALLOWED)()
    backend = client._transport._pool._network_backend
    recorder = _Recorder()
    backend._inner = recorder
    return client, backend, recorder


async def test_client_never_follows_redirects_or_proxies():
    client = guarded_http_client_factory(ALLOWED)()
    assert client.follow_redirects is False
    assert client._trust_env is False
    await client.aclose()


async def test_connect_is_pinned_to_the_checked_address(dns):
    dns["mcp.exa.ai"] = [[PUBLIC_IP]]
    client, backend, recorder = _backend()
    await backend.connect_tcp("mcp.exa.ai", 443)
    assert recorder.calls == [(PUBLIC_IP, 443)]
    await client.aclose()


async def test_connect_refuses_host_not_on_the_list(dns):
    dns["example.com"] = [[PUBLIC_IP]]
    client, backend, recorder = _backend()
    with pytest.raises(httpcore.ConnectError):
        await backend.connect_tcp("example.com", 443)
    assert recorder.calls == []
    await client.aclose()


async def test_connect_refuses_other_ports(dns):
    dns["mcp.exa.ai"] = [[PUBLIC_IP]]
    client, backend, recorder = _backend()
    with pytest.raises(httpcore.ConnectError):
        await backend.connect_tcp("mcp.exa.ai", 80)
    assert recorder.calls == []
    await client.aclose()


async def test_dns_rebinding_between_check_and_connect_is_refused(dns):
    # First answer (the save/test-time check) is public; the connect-time
    # answer is loopback. The connect resolves itself and refuses.
    dns["evil.test"] = [[PUBLIC_IP], ["127.0.0.1"]]
    await check_destination("https://evil.test/mcp", ALLOWED)
    client, backend, recorder = _backend()
    with pytest.raises(httpcore.ConnectError):
        await backend.connect_tcp("evil.test", 443)
    assert recorder.calls == []
    await client.aclose()


async def test_redirect_to_internal_host_is_not_followed(dns):
    """A 30x from an allowed server comes back as a 30x; nothing else is fetched."""
    import httpx

    dns["mcp.exa.ai"] = [[PUBLIC_IP]]
    seen: list[str] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(str(request.url))
        return httpx.Response(302, headers={"location": "http://litellm:4000/"})

    client = guarded_http_client_factory(ALLOWED)()
    client._transport = httpx.MockTransport(handler)
    response = await client.get("https://mcp.exa.ai/mcp")
    assert response.status_code == 302
    assert seen == ["https://mcp.exa.ai/mcp"]
    await client.aclose()


async def test_unix_sockets_are_refused():
    client, backend, _ = _backend()
    with pytest.raises(httpcore.ConnectError):
        await backend.connect_unix_socket("/var/run/docker.sock")
    await client.aclose()
