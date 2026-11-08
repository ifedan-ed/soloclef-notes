"""Where an MCP connection may go, checked at save, test and call time.

A URL passes only when it is ``https``, carries no credentials, uses the
default port, and names a host on the admin allowlist exactly. That host is
then resolved here and every address it answers with must be publicly
routable (``net_guard.is_public_address``), so a name on the list that is
pointed at ``10.x``, ``127.0.0.1``, a link-local address or a Docker service
is still refused.

The HTTP client every MCP connection uses (``guarded_http_client_factory``)
repeats both checks inside its TCP connect, then connects to the address it
just checked rather than letting the socket layer resolve the name again, so
an answer that changes between check and connect (DNS rebinding) cannot land
on a private address. TLS still verifies the certificate against the host
name. Redirects are not followed and proxy environment variables are ignored.
"""

from __future__ import annotations

import asyncio
import ipaddress
import socket
from collections.abc import Callable, Iterable
from typing import Any
from urllib.parse import urlsplit

import httpcore
import httpx

from app.utils.crawl.net_guard import is_public_address

HTTP_TRANSPORTS = frozenset({"streamable-http", "http", "sse"})
_HTTPS_PORT = 443


class MCPDestinationRefusedError(ValueError):
    """The URL or the address it resolves to is not an allowed MCP server."""


def normalize_host(host: str) -> str:
    return host.strip().rstrip(".").lower()


def _allowed(host: str, allowed_hosts: Iterable[str]) -> bool:
    return normalize_host(host) in {normalize_host(h) for h in allowed_hosts}


def check_url_shape(url: str, allowed_hosts: Iterable[str]) -> str:
    """The URL's host when the URL itself is acceptable; raises otherwise."""
    if not isinstance(url, str) or not url.strip():
        raise MCPDestinationRefusedError("A server URL is required")
    try:
        parts = urlsplit(url.strip())
        port = parts.port
    except ValueError as exc:
        raise MCPDestinationRefusedError("The server URL is not valid") from exc
    if parts.scheme != "https":
        raise MCPDestinationRefusedError("MCP servers must use https")
    if parts.username is not None or parts.password is not None:
        raise MCPDestinationRefusedError("The server URL must not contain credentials")
    host = parts.hostname
    if not host:
        raise MCPDestinationRefusedError("The server URL has no host")
    if port not in (None, _HTTPS_PORT):
        raise MCPDestinationRefusedError("MCP servers must use the default https port")
    try:
        ipaddress.ip_address(host)
    except ValueError:
        pass
    else:
        raise MCPDestinationRefusedError("MCP servers must be named, not an IP address")
    if not _allowed(host, allowed_hosts):
        raise MCPDestinationRefusedError(
            "This MCP server is not on the allowed list. Ask an administrator."
        )
    return normalize_host(host)


async def _getaddrinfo(host: str, port: int) -> list[str]:
    loop = asyncio.get_running_loop()
    infos = await loop.getaddrinfo(
        host, port, type=socket.SOCK_STREAM, proto=socket.IPPROTO_TCP
    )
    seen: list[str] = []
    for info in infos:
        address = info[4][0]
        if address not in seen:
            seen.append(address)
    return seen


async def resolve_public(host: str, port: int = _HTTPS_PORT) -> list[str]:
    """Every address ``host`` resolves to; raises unless all are public."""
    try:
        addresses = await _getaddrinfo(host, port)
    except (OSError, UnicodeError) as exc:
        raise MCPDestinationRefusedError(
            "The server host could not be resolved"
        ) from exc
    if not addresses:
        raise MCPDestinationRefusedError("The server host could not be resolved")
    if not all(is_public_address(a) for a in addresses):
        raise MCPDestinationRefusedError(
            "The server host resolves to a private address"
        )
    return addresses


async def check_destination(url: str, allowed_hosts: Iterable[str]) -> str:
    """Shape and resolution checks together; returns the normalized host."""
    host = check_url_shape(url, allowed_hosts)
    await resolve_public(host)
    return host


class _PinnedNetworkBackend(httpcore.AsyncNetworkBackend):
    """Connects only to allowlisted hosts, at an address checked as public."""

    def __init__(self, allowed_hosts: Iterable[str]):
        self._allowed_hosts = frozenset(normalize_host(h) for h in allowed_hosts)
        self._inner = httpcore.AnyIOBackend()

    async def connect_tcp(
        self,
        host: str,
        port: int,
        timeout: float | None = None,
        local_address: str | None = None,
        socket_options: Iterable[Any] | None = None,
    ) -> httpcore.AsyncNetworkStream:
        name = normalize_host(host)
        if name not in self._allowed_hosts or port != _HTTPS_PORT:
            raise httpcore.ConnectError("MCP destination refused")
        try:
            addresses = await resolve_public(name, port)
        except MCPDestinationRefusedError as exc:
            raise httpcore.ConnectError("MCP destination refused") from exc
        # IPv4 first: the backend container may have no IPv6 route.
        ordered = sorted(addresses, key=lambda a: ":" in a)
        last_error: Exception | None = None
        for address in ordered:
            try:
                return await self._inner.connect_tcp(
                    address,
                    port,
                    timeout=timeout,
                    local_address=local_address,
                    socket_options=socket_options,
                )
            except (httpcore.ConnectError, httpcore.ConnectTimeout, OSError) as exc:
                last_error = exc
        raise httpcore.ConnectError("MCP server unreachable") from last_error

    async def connect_unix_socket(self, *args: Any, **kwargs: Any):
        raise httpcore.ConnectError("MCP destination refused")

    async def sleep(self, seconds: float) -> None:
        await self._inner.sleep(seconds)


def guarded_http_client_factory(
    allowed_hosts: Iterable[str],
) -> Callable[..., httpx.AsyncClient]:
    """An ``httpx_client_factory`` for ``streamablehttp_client``."""
    hosts = frozenset(normalize_host(h) for h in allowed_hosts)

    def factory(
        headers: dict[str, str] | None = None,
        timeout: httpx.Timeout | None = None,
        auth: httpx.Auth | None = None,
    ) -> httpx.AsyncClient:
        transport = httpx.AsyncHTTPTransport(trust_env=False, retries=0)
        # httpx does not take a network backend, so the pool it built gets one.
        transport._pool._network_backend = _PinnedNetworkBackend(hosts)
        kwargs: dict[str, Any] = {
            "transport": transport,
            "follow_redirects": False,
            "trust_env": False,
            "timeout": timeout or httpx.Timeout(30.0, read=300.0),
        }
        if headers is not None:
            kwargs["headers"] = headers
        if auth is not None:
            kwargs["auth"] = auth
        return httpx.AsyncClient(**kwargs)

    return factory
