"""SSRF guard (resolve-then-pin) for the IMAP connect target.

``_resolve_pinned_address`` is exercised directly — the literal-address
cases need no resolver at all; the hostname cases stub ``socket.getaddrinfo``
because a unit test cannot depend on what DNS answers (mirrors
``tests/unit/utils/crawl/test_net_guard.py``). ``mail.example.com`` is
checked against the real resolver, skipped if it cannot be reached, since it
is the one host this guard must still allow.
"""

from __future__ import annotations

import socket

import pytest

from app.connectors.email_imap import client as imap_client
from app.connectors.email_imap.client import (
    ImapAccountSettings,
    ImapConnectionError,
    ImapDestinationRefusedError,
    ReadOnlyImapClient,
    _resolve_pinned_address,
)

pytestmark = pytest.mark.unit


def test_public_ip_literal_is_pinned_to_itself():
    assert _resolve_pinned_address("93.184.216.34") == "93.184.216.34"


@pytest.mark.parametrize(
    "literal",
    [
        "127.0.0.1",  # loopback
        "10.0.0.1",  # RFC1918
        "172.17.0.1",  # RFC1918 / default docker bridge
        "192.168.1.1",  # RFC1918
        "169.254.169.254",  # cloud metadata
        "100.64.0.1",  # CGNAT, RFC 6598
        "0.0.0.0",  # unspecified
        "224.0.0.1",  # multicast
        "::1",  # IPv6 loopback
        "fc00::1",  # IPv6 unique-local
    ],
)
def test_non_public_ip_literals_are_refused(literal):
    with pytest.raises(ImapDestinationRefusedError):
        _resolve_pinned_address(literal)


def test_hostname_resolving_to_a_private_address_is_refused(monkeypatch):
    """Stands in for an internal Docker service name (db/redis/mcp/litellm/
    backend/searxng): it has no public DNS record and resolves to a private
    container address, which this same check already rejects."""
    monkeypatch.setattr(
        imap_client.socket,
        "getaddrinfo",
        lambda *a, **k: [(socket.AF_INET, None, None, "", ("172.20.0.5", 0))],
    )
    with pytest.raises(ImapDestinationRefusedError):
        _resolve_pinned_address("redis")


def test_hostname_resolving_only_to_public_addresses_is_pinned(monkeypatch):
    monkeypatch.setattr(
        imap_client.socket,
        "getaddrinfo",
        lambda *a, **k: [(socket.AF_INET, None, None, "", ("93.184.216.34", 0))],
    )
    assert _resolve_pinned_address("mail.example.com") == "93.184.216.34"


def test_hostname_with_a_mixed_public_and_private_answer_is_refused(monkeypatch):
    """A host that answers with both a public and a private address is
    refused: a second resolution (by anything downstream) might pick either."""
    monkeypatch.setattr(
        imap_client.socket,
        "getaddrinfo",
        lambda *a, **k: [
            (socket.AF_INET, None, None, "", ("93.184.216.34", 0)),
            (socket.AF_INET, None, None, "", ("10.0.0.1", 0)),
        ],
    )
    with pytest.raises(ImapDestinationRefusedError):
        _resolve_pinned_address("split-horizon.example.com")


def test_unresolvable_hostname_is_refused(monkeypatch):
    def _boom(*a, **k):
        raise socket.gaierror("Name or service not known")

    monkeypatch.setattr(imap_client.socket, "getaddrinfo", _boom)
    with pytest.raises(ImapDestinationRefusedError):
        _resolve_pinned_address("does-not-exist.example")


def test_ipv4_first_when_a_hostname_has_both_families(monkeypatch):
    monkeypatch.setattr(
        imap_client.socket,
        "getaddrinfo",
        lambda *a, **k: [
            (socket.AF_INET6, None, None, "", ("2606:2800:220:1:248:1893:25c8:1946", 0)),
            (socket.AF_INET, None, None, "", ("93.184.216.34", 0)),
        ],
    )
    assert _resolve_pinned_address("example.com") == "93.184.216.34"


def _mail_example_com_reachable() -> bool:
    try:
        socket.getaddrinfo("mail.example.com", None, proto=socket.IPPROTO_TCP)
        return True
    except OSError:
        return False


def test_enter_gives_the_same_message_for_a_refused_destination_and_a_refused_connect(
    monkeypatch,
):
    """A refused-by-the-guard destination and a plain TCP refusal must read
    identically — a distinct message per cause would let this endpoint be
    used to port-scan the backend's network namespace."""
    settings = ImapAccountSettings(
        host="internal.example.test", port=993, use_tls=True,
        username="u", password="p",
    )
    monkeypatch.setattr(
        imap_client, "_resolve_pinned_address",
        lambda _host: (_ for _ in ()).throw(ImapDestinationRefusedError(imap_client._REFUSED_MESSAGE)),
    )
    with pytest.raises(ImapConnectionError) as refused_exc, ReadOnlyImapClient(settings):
        pass

    def _boom_connect(*a, **k):
        raise OSError("Connection refused")

    monkeypatch.setattr(
        imap_client, "_resolve_pinned_address", lambda _host: "93.184.216.34"
    )
    monkeypatch.setattr(imap_client.socket, "create_connection", _boom_connect)
    with pytest.raises(ImapConnectionError) as tcp_refused_exc, ReadOnlyImapClient(settings):
        pass

    assert str(refused_exc.value) == str(tcp_refused_exc.value) == imap_client._REFUSED_MESSAGE


@pytest.mark.skipif(
    not _mail_example_com_reachable(), reason="no DNS resolution available here"
)
def test_the_owners_mail_server_is_allowed():
    """The allow-list exception the task calls out: mail.example.com is a
    public address, not an internal one, so the guard must not refuse it."""
    address = _resolve_pinned_address("mail.example.com")
    assert address  # resolved to *something*
    import ipaddress as ip_mod

    assert ip_mod.ip_address(address).is_global
