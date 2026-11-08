"""A member's own MCP server: any public https host, never an internal one.

The allow-list is for site servers (an administrator's); a workspace Owner or
Editor's server may sit on any public host, but every other check stays:
https on the default port, a real domain name, public addresses only, and the
connection pinned to its own host.
"""

from __future__ import annotations

from types import SimpleNamespace

import httpcore
import pytest

from app.mcp_security import allowlist, server_origin, url_guard
from app.mcp_security.server_origin import (
    MEMBER,
    ORIGIN_KEY,
    SITE,
    origin_for_new_server,
    origin_of,
    own_host,
    reachable_hosts,
    reachable_hosts_for_save,
)
from app.mcp_security.url_guard import (
    MCPDestinationRefusedError,
    check_destination,
    guarded_http_client_factory,
)

pytestmark = pytest.mark.unit

PUBLIC_IP = "93.184.215.14"


@pytest.fixture
def dns(monkeypatch):
    answers: dict[str, list[str]] = {}

    async def fake(host: str, port: int) -> list[str]:
        if host not in answers:
            raise OSError("NXDOMAIN")
        return answers[host]

    monkeypatch.setattr(url_guard, "_getaddrinfo", fake)
    return answers


def test_an_administrators_server_is_a_site_server_and_anyone_elses_their_own():
    admin = SimpleNamespace(is_superuser=True, email="x@y.z")
    member = SimpleNamespace(is_superuser=False, email="someone@example.com")
    assert origin_for_new_server(admin) == SITE
    assert origin_for_new_server(member) == MEMBER


@pytest.mark.parametrize(
    ("config", "origin"),
    [
        (None, SITE),
        ({}, SITE),  # rows from before the split: the shared three
        ({ORIGIN_KEY: "site"}, SITE),
        ({ORIGIN_KEY: "member"}, MEMBER),
        ({ORIGIN_KEY: "MEMBER"}, SITE),  # anything unexpected fails closed
        ({ORIGIN_KEY: True}, SITE),
    ],
)
def test_the_stored_origin_fails_closed(config, origin):
    assert origin_of(config) == origin


def test_a_member_server_reaches_only_its_own_host():
    assert own_host("https://MCP.Example.com./mcp") == {"mcp.example.com"}
    assert own_host("https://litellm:4000/") == frozenset()
    assert own_host("https://printer.local/mcp") == frozenset()
    assert own_host("not a url") == frozenset()


async def test_member_hosts_ignore_the_allow_list(monkeypatch):
    async def never():
        raise AssertionError("the allow-list is not read for a member's server")

    monkeypatch.setattr(server_origin, "current_allowed_hosts", never)
    assert await reachable_hosts(MEMBER, "https://mcp.notion.com/mcp") == {
        "mcp.notion.com"
    }


async def test_site_hosts_are_the_allow_list(monkeypatch):
    async def listed():
        return frozenset({"mcp.exa.ai"})

    monkeypatch.setattr(server_origin, "current_allowed_hosts", listed)
    assert await reachable_hosts(SITE, "https://mcp.notion.com/mcp") == {"mcp.exa.ai"}


async def test_a_public_https_server_is_accepted_for_a_member(dns):
    dns["mcp.notion.com"] = [PUBLIC_IP]
    url = "https://mcp.notion.com/mcp"
    hosts = await reachable_hosts_for_save(None, MEMBER, url)
    assert await check_destination(url, hosts) == "mcp.notion.com"


async def test_the_same_server_is_refused_as_a_site_server_off_the_list(dns):
    dns["mcp.notion.com"] = [PUBLIC_IP]
    allowlist.reset_cache()

    class Session:
        async def get(self, _model, _key):
            return None  # no row: the default three

    hosts = await reachable_hosts_for_save(
        Session(), SITE, "https://mcp.notion.com/mcp"
    )
    with pytest.raises(MCPDestinationRefusedError):
        await check_destination("https://mcp.notion.com/mcp", hosts)


@pytest.mark.parametrize(
    ("url", "addresses"),
    [
        ("http://mcp.example.com/mcp", [PUBLIC_IP]),  # not https
        ("https://mcp.example.com:8443/mcp", [PUBLIC_IP]),  # not the default port
        ("https://user:pw@mcp.example.com/mcp", [PUBLIC_IP]),  # credentials
        ("https://93.184.215.14/mcp", [PUBLIC_IP]),  # an IP, not a name
        ("https://mcp.example.com/mcp", ["10.0.0.5"]),
        ("https://mcp.example.com/mcp", ["172.18.0.4"]),  # a Docker network
        ("https://mcp.example.com/mcp", ["192.168.1.20"]),
        ("https://mcp.example.com/mcp", ["127.0.0.1"]),
        ("https://mcp.example.com/mcp", ["169.254.169.254"]),  # cloud metadata
        ("https://mcp.example.com/mcp", ["100.64.0.7"]),  # tailnet / CGNAT
        ("https://mcp.example.com/mcp", ["::1"]),
        ("https://mcp.example.com/mcp", [PUBLIC_IP, "10.0.0.5"]),
    ],
)
async def test_internal_or_malformed_destinations_are_refused_for_a_member(
    dns, url, addresses
):
    dns["mcp.example.com"] = addresses
    dns["93.184.215.14"] = addresses
    with pytest.raises(MCPDestinationRefusedError):
        hosts = await reachable_hosts_for_save(None, MEMBER, url)
        await check_destination(url, hosts)


@pytest.mark.parametrize(
    "url",
    [
        "https://litellm/mcp",
        "https://backend:8000/mcp",
        "https://localhost/mcp",
        "https://nas.local/mcp",
        "https://grafana.internal/mcp",
    ],
)
async def test_internal_names_are_refused_before_any_lookup(dns, url):
    with pytest.raises(MCPDestinationRefusedError, match="public address"):
        await reachable_hosts_for_save(None, MEMBER, url)


async def test_a_member_connection_cannot_be_steered_to_another_host(dns):
    dns["mcp.example.com"] = [PUBLIC_IP]
    dns["other.example.com"] = [PUBLIC_IP]
    factory = guarded_http_client_factory(own_host("https://mcp.example.com/mcp"))
    client = factory()
    backend = client._transport._pool._network_backend
    with pytest.raises(httpcore.ConnectError):
        await backend.connect_tcp("other.example.com", 443)
    await client.aclose()


async def test_a_rebound_name_is_refused_at_connect_time(dns):
    dns["mcp.example.com"] = ["10.0.0.9"]  # was public when saved, now internal
    factory = guarded_http_client_factory(own_host("https://mcp.example.com/mcp"))
    client = factory()
    backend = client._transport._pool._network_backend
    with pytest.raises(httpcore.ConnectError):
        await backend.connect_tcp("mcp.example.com", 443)
    await client.aclose()
