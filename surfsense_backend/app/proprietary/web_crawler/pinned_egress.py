# SurfSense proprietary crawler engine.
#
# Part of the ``app.proprietary`` package; licensed separately from the
# Apache-2.0 project root (see ``app/proprietary/LICENSE``).
"""Every connection a crawl makes goes to the address that was checked.

``crawl_url`` and ``destination_guard`` judge a URL by resolving its host
(``net_guard.is_publicly_routable``), and the fetcher then resolved the host
again on its own: a name that answers a public address to the check and a
private one to the fetch (DNS rebinding) read this server's internal pages.
The browser tiers had more ways past the check: Chromium follows an HTTP
redirect without asking ``page.route``, and WebSocket connections and a
service worker's fetches never reach it.

So when no proxy of the operator's is configured (``PROXY_URL`` and the
provider settings, ``app/utils/proxy``), every tier connects through a
SOCKS5 proxy on 127.0.0.1 opened for that one fetch (``PinnedEgress``):

* the client sends the host name (Chromium always does for ``socks5://``,
  curl for ``socks5h://``), the proxy resolves it once, refuses unless
  every address it gets is public (``net_guard.is_public_address``), and
  connects to those same addresses: the address checked is the address
  connected to, whatever the name answers next;
* that covers every TCP connection the fetch makes: redirect hops, script
  navigations, subresources, WebSockets and service workers alike;
* a refusal answers "connection not allowed by ruleset" (SOCKS reply 2),
  is counted on that fetch's proxy (``refused``), and a fetch that failed
  after one ends the crawl as restricted (``raise_if_refused``).

With the operator's proxy configured, the fetch goes through it instead and
that proxy resolves and connects, from wherever it runs: this server's
internal addresses are out of its reach only when it runs elsewhere (custom
/README.md, "Security fixes of 2026-10-11", follow-up).

Limits: CONNECT only (no BIND, no UDP: Chromium uses neither for a proxied
page, and QUIC is off behind a SOCKS proxy); a connection attempt waits at
most ``CONNECT_TIMEOUT_SECONDS`` (10 s) per address; at most
``MAX_CONNECTIONS_PER_FETCH`` (256) connections at once per fetch, more are
refused; the proxy closes when the fetch ends, and every connection with it.
The proxies of one process share one thread and event loop, started on first
use.
"""

from __future__ import annotations

import asyncio
import ipaddress
import logging
import os
import socket
import struct
import threading
from collections.abc import Awaitable, Callable
from concurrent.futures import Future

from app.utils.crawl.net_guard import is_public_address

logger = logging.getLogger(__name__)

#: How long one connection attempt to a vetted address may take.
CONNECT_TIMEOUT_SECONDS = 10.0
#: Connections one fetch may hold open at once; more are refused.
MAX_CONNECTIONS_PER_FETCH = 256
#: How long the client may take to send its SOCKS greeting and request.
HANDSHAKE_TIMEOUT_SECONDS = 10.0
_PIPE_CHUNK = 64 * 1024

_SOCKS_VERSION = 5
_NO_AUTH = 0
_NO_ACCEPTABLE_METHOD = 0xFF
_CONNECT = 1
_ATYP_IPV4, _ATYP_DOMAIN, _ATYP_IPV6 = 1, 3, 4
_REPLY_OK = 0
_REPLY_NOT_ALLOWED = 2
_REPLY_HOST_UNREACHABLE = 4
_REPLY_COMMAND_NOT_SUPPORTED = 7
_REPLY_ADDRESS_NOT_SUPPORTED = 8

Resolver = Callable[[str, int], Awaitable[list[str]]]


class DestinationRefusedError(Exception):
    """The host is not public, or did not resolve."""


async def _resolve(host: str, port: int) -> list[str]:
    """Every address ``host`` has for TCP, in the resolver's order."""
    infos = await asyncio.get_running_loop().getaddrinfo(
        host, port, type=socket.SOCK_STREAM
    )
    seen: list[str] = []
    for info in infos:
        address = info[4][0]
        if address not in seen:
            seen.append(address)
    return seen


class _EventLoopThread:
    """The one loop every pinned proxy of this process runs on."""

    def __init__(self) -> None:
        self._loop: asyncio.AbstractEventLoop | None = None
        self._pid = 0
        self._lock = threading.Lock()

    def loop(self) -> asyncio.AbstractEventLoop:
        with self._lock:
            # A forked child (a Celery worker) inherits the loop but not the
            # thread running it: it starts its own.
            if self._loop is None or self._loop.is_closed() or self._pid != os.getpid():
                loop = asyncio.new_event_loop()
                ready = threading.Event()

                def run() -> None:
                    asyncio.set_event_loop(loop)
                    loop.call_soon(ready.set)
                    loop.run_forever()

                threading.Thread(
                    target=run, name="crawler-pinned-egress", daemon=True
                ).start()
                ready.wait()
                self._loop = loop
                self._pid = os.getpid()
            return self._loop

    def run(self, coro) -> Future:
        return asyncio.run_coroutine_threadsafe(coro, self.loop())


_LOOP = _EventLoopThread()


class PinnedEgress:
    """One fetch's SOCKS5 proxy on 127.0.0.1; ``with`` closes it.

    ``resolve`` and ``is_public`` are seams for tests; the crawler passes
    neither (the system resolver, ``net_guard.is_public_address``).
    """

    def __init__(
        self,
        *,
        resolve: Resolver | None = None,
        is_public: Callable[[str], bool] | None = None,
        connect_timeout: float = CONNECT_TIMEOUT_SECONDS,
    ) -> None:
        self._resolve = resolve or _resolve
        self._is_public = is_public or is_public_address
        self._connect_timeout = connect_timeout
        self._server: asyncio.base_events.Server | None = None
        self._tasks: set[asyncio.Task] = set()
        self.port = 0
        #: Connections refused because their host was not public.
        self.refused = 0

    #: This fetch connects through it (``_NoEgress`` says False).
    pinned = True

    # --- lifetime ---------------------------------------------------------

    def open(self) -> PinnedEgress:
        self._server = _LOOP.run(self._start()).result(timeout=10)
        self.port = self._server.sockets[0].getsockname()[1]
        return self

    async def _start(self):
        return await asyncio.start_server(self._serve, host="127.0.0.1", port=0)

    def close(self) -> None:
        if self._server is not None:
            _LOOP.run(self._stop()).result(timeout=10)
            self._server = None

    async def _stop(self) -> None:
        assert self._server is not None
        self._server.close()
        for task in list(self._tasks):
            task.cancel()
        await asyncio.gather(*self._tasks, return_exceptions=True)
        await self._server.wait_closed()

    def __enter__(self) -> PinnedEgress:
        return self.open()

    def __exit__(self, *exc) -> None:
        self.close()

    async def __aenter__(self) -> PinnedEgress:
        # From another event loop (the static tier's): wait without blocking it.
        self._server = await asyncio.wrap_future(_LOOP.run(self._start()))
        self.port = self._server.sockets[0].getsockname()[1]
        return self

    async def __aexit__(self, *exc) -> None:
        if self._server is not None:
            await asyncio.wrap_future(_LOOP.run(self._stop()))
            self._server = None

    @property
    def browser_proxy(self) -> str:
        """For Chromium (Playwright's proxy): it sends host names to a SOCKS5 proxy."""
        return f"socks5://127.0.0.1:{self.port}"

    @property
    def curl_proxy(self) -> str:
        """For curl: ``socks5h`` makes it send the host name rather than resolve it."""
        return f"socks5h://127.0.0.1:{self.port}"

    def raise_if_refused(self, error: BaseException | None = None) -> None:
        """After a failed fetch: was it because a destination was refused here?"""
        from app.proprietary.web_crawler.destination_guard import (
            RestrictedDestinationError,
        )

        if self.refused:
            raise RestrictedDestinationError(
                "the fetch was sent to a host that is not publicly routable"
            ) from error

    # --- one connection -----------------------------------------------------

    async def _serve(self, reader, writer) -> None:
        task = asyncio.current_task()
        if task is not None:
            self._tasks.add(task)
        try:
            if len(self._tasks) > MAX_CONNECTIONS_PER_FETCH:
                return
            await self._handshake_and_pipe(reader, writer)
        except (asyncio.IncompleteReadError, ConnectionError, TimeoutError):
            pass
        except asyncio.CancelledError:
            pass
        except Exception:
            logger.debug("[webcrawler] pinned egress connection failed", exc_info=True)
        finally:
            if task is not None:
                self._tasks.discard(task)
            writer.close()

    async def _handshake_and_pipe(self, reader, writer) -> None:
        host, port = await asyncio.wait_for(
            self._request(reader, writer), timeout=HANDSHAKE_TIMEOUT_SECONDS
        )
        if host is None:
            return
        try:
            addresses = await self._vetted(host, port)
        except DestinationRefusedError:
            self.refused += 1
            logger.info("[webcrawler] refused a connection to a non-public host")
            await self._reply(writer, _REPLY_NOT_ALLOWED)
            return
        upstream = await self._connect(addresses, port)
        if upstream is None:
            await self._reply(writer, _REPLY_HOST_UNREACHABLE)
            return
        up_reader, up_writer = upstream
        await self._reply(writer, _REPLY_OK)
        try:
            await asyncio.gather(_pipe(reader, up_writer), _pipe(up_reader, writer))
        finally:
            up_writer.close()

    async def _request(self, reader, writer) -> tuple[str | None, int]:
        version, count = await reader.readexactly(2)
        methods = await reader.readexactly(count)
        if version != _SOCKS_VERSION or _NO_AUTH not in methods:
            writer.write(bytes([_SOCKS_VERSION, _NO_ACCEPTABLE_METHOD]))
            await writer.drain()
            return None, 0
        writer.write(bytes([_SOCKS_VERSION, _NO_AUTH]))
        await writer.drain()

        version, command, _reserved, kind = await reader.readexactly(4)
        if kind == _ATYP_IPV4:
            host = socket.inet_ntop(socket.AF_INET, await reader.readexactly(4))
        elif kind == _ATYP_IPV6:
            host = socket.inet_ntop(socket.AF_INET6, await reader.readexactly(16))
        elif kind == _ATYP_DOMAIN:
            (length,) = await reader.readexactly(1)
            host = (await reader.readexactly(length)).decode("idna")
        else:
            await self._reply(writer, _REPLY_ADDRESS_NOT_SUPPORTED)
            return None, 0
        (port,) = struct.unpack("!H", await reader.readexactly(2))
        if version != _SOCKS_VERSION or command != _CONNECT:
            await self._reply(writer, _REPLY_COMMAND_NOT_SUPPORTED)
            return None, 0
        return host, port

    async def _vetted(self, host: str, port: int) -> list[str]:
        """The addresses to connect to: all public, or ``DestinationRefusedError``."""
        try:
            ipaddress.ip_address(host)
        except ValueError:
            try:
                addresses = await self._resolve(host, port)
            except (OSError, UnicodeError) as exc:
                raise DestinationRefusedError(host) from exc
        else:
            addresses = [host]
        # Every answer, as net_guard judges a URL: a name with one private
        # answer among public ones is refused, not tried on the public one.
        if not addresses or not all(self._is_public(a) for a in addresses):
            raise DestinationRefusedError(host)
        return addresses

    async def _connect(self, addresses: list[str], port: int):
        for address in addresses:
            try:
                return await asyncio.wait_for(
                    asyncio.open_connection(address, port),
                    timeout=self._connect_timeout,
                )
            except (OSError, TimeoutError):
                continue
        return None

    @staticmethod
    async def _reply(writer, code: int) -> None:
        # Bound address 0.0.0.0:0: clients ignore it for CONNECT.
        writer.write(bytes([_SOCKS_VERSION, code, 0, _ATYP_IPV4, 0, 0, 0, 0, 0, 0]))
        await writer.drain()


async def _pipe(reader, writer) -> None:
    try:
        while data := await reader.read(_PIPE_CHUNK):
            writer.write(data)
            await writer.drain()
    except (ConnectionError, asyncio.CancelledError):
        pass
    finally:
        try:
            if writer.can_write_eof():
                writer.write_eof()
        except (OSError, RuntimeError):
            pass


class _NoEgress:
    """The operator's proxy is set: it makes the connections, nothing to open."""

    browser_proxy = curl_proxy = None
    refused = 0
    pinned = False

    def raise_if_refused(self, error: BaseException | None = None) -> None:
        return None

    def __enter__(self):
        return self

    def __exit__(self, *exc) -> None:
        return None

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc) -> None:
        return None


def egress_for(operator_proxy: str | None) -> PinnedEgress | _NoEgress:
    """This fetch's way out: a pinned proxy, unless the operator configured one."""
    return _NoEgress() if operator_proxy else PinnedEgress()
