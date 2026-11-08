"""Follow a URL's redirects without ever connecting to a private address.

Before the crawler reads a pasted page (the fallback when Browserbase is not
used), the redirect chain is walked here: every hop must be http(s), its host
must resolve to public addresses only, and the connection goes to the very
address that was checked (no second DNS lookup to race), with TLS still
verified against the host name (the MCP guard's ``resolve_public``). Redirects are followed by hand, at most
``MAX_REDIRECTS``, each hop checked the same way. The crawler is then given
the final URL, which it checks again before fetching.
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any
from urllib.parse import urljoin, urlsplit

import httpcore
import httpx

from app.mcp_security.url_guard import (
    MCPDestinationRefusedError,
    resolve_public as _resolve_public,
)

MAX_REDIRECTS = 5
TIMEOUT_S = 15.0
_HEADERS = {
    "User-Agent": (
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) "
        "Chrome/126.0 Safari/537.36"
    ),
    "Accept": "text/html,application/xhtml+xml,*/*;q=0.8",
}


class NotPublicError(Exception):
    """A hop is not http(s) or does not resolve to public addresses only."""


async def resolve_public(host: str, port: int) -> list[str]:
    """Every address ``host`` answers with, all public (the MCP guard's check)."""
    try:
        return await _resolve_public(host, port)
    except MCPDestinationRefusedError as exc:
        raise NotPublicError(f"{host} is not a public address") from exc


class _PublicOnlyBackend(httpcore.AsyncNetworkBackend):
    """Resolves, checks every answer is public, connects to a checked address."""

    def __init__(self) -> None:
        self._inner = httpcore.AnyIOBackend()

    async def connect_tcp(
        self,
        host: str,
        port: int,
        timeout: float | None = None,
        local_address: str | None = None,
        socket_options: Iterable[Any] | None = None,
    ) -> httpcore.AsyncNetworkStream:
        try:
            addresses = await resolve_public(host, port)
        except NotPublicError as exc:
            raise httpcore.ConnectError(str(exc)) from exc
        last: Exception | None = None
        # IPv4 first: the container may have no IPv6 route.
        for address in sorted(addresses, key=lambda a: ":" in a):
            try:
                return await self._inner.connect_tcp(
                    address,
                    port,
                    timeout=timeout,
                    local_address=local_address,
                    socket_options=socket_options,
                )
            except (httpcore.ConnectError, httpcore.ConnectTimeout, OSError) as exc:
                last = exc
        raise httpcore.ConnectError(f"{host} unreachable") from last

    async def connect_unix_socket(self, *args: Any, **kwargs: Any):
        raise httpcore.ConnectError("refused")

    async def sleep(self, seconds: float) -> None:
        await self._inner.sleep(seconds)


def public_only_client() -> httpx.AsyncClient:
    transport = httpx.AsyncHTTPTransport(trust_env=False, retries=0)
    transport._pool._network_backend = _PublicOnlyBackend()
    return httpx.AsyncClient(
        transport=transport,
        follow_redirects=False,
        trust_env=False,
        timeout=TIMEOUT_S,
        headers=_HEADERS,
    )


async def _check_hop(url: str) -> None:
    parts = urlsplit(url)
    if parts.scheme not in ("http", "https") or not parts.hostname:
        raise NotPublicError("not an http(s) address")
    if parts.username is not None or parts.password is not None:
        raise NotPublicError("addresses with credentials are not read")
    try:
        port = parts.port or (443 if parts.scheme == "https" else 80)
    except ValueError as exc:
        raise NotPublicError("not a valid address") from exc
    await resolve_public(parts.hostname, port)


async def final_public_url(url: str, *, client: httpx.AsyncClient | None = None) -> str:
    """Where ``url`` ends up, every hop public; raises :class:`NotPublicError`.

    A server that cannot be reached (or answers an error) is not an address
    problem: the URL reached so far is returned and the reader decides.
    """
    own = client is None
    client = client or public_only_client()
    try:
        for _ in range(MAX_REDIRECTS + 1):
            await _check_hop(url)
            try:
                async with client.stream("GET", url) as response:
                    location = response.headers.get("location")
                    redirected = 300 <= response.status_code < 400 and location
            except httpx.HTTPError:
                # Unreachable or broken, not private: let the reader decide.
                return url
            if not redirected:
                return url
            url = urljoin(url, location)
        raise NotPublicError("too many redirects")
    finally:
        if own:
            await client.aclose()
