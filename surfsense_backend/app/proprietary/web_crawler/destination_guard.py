# SurfSense proprietary crawler engine.
#
# This module is part of the ``app.proprietary`` package and is licensed
# SEPARATELY from the Apache-2.0 project root. See ``app/proprietary/LICENSE``.
# Do not relicense or redistribute this file under Apache-2.0.
"""Keep every fetch of a crawl on public addresses, not only its first URL.

``crawl_url`` checks the URL it is given (``net_guard.is_publicly_routable``)
before any tier runs. The tiers then follow redirects, and the two browser
tiers run the page's scripts in a Chromium on this server, inside its
network: a page could send the fetch on to ``http://169.254.169.254/`` or to
another container. So:

* Every connection of every tier goes through ``pinned_egress`` when no
  proxy of the operator's is configured: a SOCKS5 proxy on 127.0.0.1 for
  that fetch, which resolves each host once, refuses unless every address is
  public, and connects to those same addresses. That is the defence against
  DNS rebinding (a name answering a public address to the checks here and a
  private one to the fetcher), and it also stops what never reaches
  ``page.route``: Chromium's HTTP redirect hops, WebSockets, a service
  worker's fetches. A fetch that failed after a refusal there ends the crawl.
* The static tier's curl, with the operator's proxy, follows redirects in
  its "safe" mode (Scrapling's default), which refuses a redirect to an
  internal address. That refusal becomes ``RestrictedDestinationError`` and
  ends the crawl: the browser tiers would have followed the same redirect.
  Through the pinned proxy it follows every redirect and the proxy judges
  each hop ("safe" judges the address curl is connected to, the proxy's).
  The URL curl ended on is checked too.
* The browser tiers get ``BrowserDestinationGuard``: before the page loads
  (Scrapling's ``page_setup``) every request the page makes to a host that
  is not public is aborted, navigations in any frame and subresources alike,
  the page's and its service workers' (whose requests reach only the
  context's route, Playwright 1.61), and each main-frame navigation is
  noted; after the load, the final URL and every main-frame navigation are
  checked (as ``notebooks/research/page_read/browser.py`` does), and a page
  that went through a non-public host is refused. The DynamicFetcher tier
  also closes a WebSocket to a host that is not public
  (``page.route_web_socket``); the StealthyFetcher tier does not, since
  Playwright does that by replacing the page's ``WebSocket``, which a bot
  check can see; its WebSockets meet the pinned proxy, or the operator's.

Chromium follows an HTTP redirect without asking the page's route (seen with
Playwright 1.61): through the pinned proxy such a hop to a private host is
refused before it is requested; through the operator's proxy it is
requested from wherever that proxy runs, and the check after the load
refuses what came back. Each host is resolved once per fetch for these
checks.
"""

from __future__ import annotations

import logging
import re
from collections.abc import Callable
from typing import Any
from urllib.parse import urlsplit

from app.utils.crawl import is_publicly_routable

logger = logging.getLogger(__name__)

#: How curl's "safe" redirect mode words its refusal (curl_cffi, error 7):
#: "Redirect to internal IP 10.0.0.1 rejected (SSRF protection)".
_CURL_REFUSAL_MARKERS = ("SSRF protection", "Redirect to internal IP")
_CHECKED_SCHEMES = frozenset({"http", "https"})
_WEB_SOCKET_SCHEMES = {"ws": "http", "wss": "https"}


class RestrictedDestinationError(Exception):
    """A fetch went, or was about to go, to a host that is not publicly routable."""


def is_refused_redirect(exc: BaseException) -> bool:
    """True when the static fetch failed because curl refused an internal redirect."""
    text = str(exc)
    return any(marker in text for marker in _CURL_REFUSAL_MARKERS)


def ensure_public(
    url: str | None, is_public: Callable[[str], bool] = is_publicly_routable
) -> None:
    """Raise unless ``url`` (an http(s) URL a fetch ended on) is public."""
    if not url or urlsplit(url).scheme not in _CHECKED_SCHEMES:
        return
    if not is_public(url):
        raise RestrictedDestinationError(
            "the page led to a host that is not publicly routable"
        )


class BrowserDestinationGuard:
    """One browser fetch's guard: aborts requests to non-public hosts, then checks the load."""

    def __init__(
        self,
        is_public: Callable[[str], bool] = is_publicly_routable,
        *,
        close_private_web_sockets: bool = False,
    ) -> None:
        self._is_public = is_public
        self._close_private_web_sockets = close_private_web_sockets
        self._hosts: dict[str, bool] = {}
        self.main_frame_urls: list[str] = []
        #: Main-frame navigations aborted here (the page tried to move there).
        self.refused_navigations: list[str] = []

    def _public(self, url: str) -> bool:
        parts = urlsplit(url)
        if parts.scheme in _WEB_SOCKET_SCHEMES:
            # Judged as the http(s) URL its handshake is.
            parts = parts._replace(scheme=_WEB_SOCKET_SCHEMES[parts.scheme])
            url = parts.geturl()
        if parts.scheme not in _CHECKED_SCHEMES:
            # data:, blob:, about: never leave the browser.
            return True
        host = (parts.hostname or "").lower()
        if not host:
            return False
        known = self._hosts.get(host)
        if known is None:
            try:
                known = bool(self._is_public(url))
            except Exception:
                known = False
            self._hosts[host] = known
        return known

    def setup(self, page: Any) -> None:
        """Scrapling's ``page_setup``: runs before the first navigation."""
        page.route("**/*", self._on_request)
        # A service worker's requests reach only the context's routes.
        context = getattr(page, "context", None)
        if context is not None:
            context.route("**/*", self._on_request)
        route_web_socket = getattr(page, "route_web_socket", None)
        if self._close_private_web_sockets and route_web_socket is not None:
            route_web_socket(re.compile(r".*"), self._on_web_socket)
        page.on("framenavigated", self._on_navigated)

    def _on_web_socket(self, ws: Any) -> None:
        if self._public(ws.url):
            ws.connect_to_server()
            return
        logger.info("[webcrawler] closed a WebSocket to a non-public host")
        ws.close()

    def _on_request(self, route: Any) -> None:
        request = route.request
        url = request.url
        if self._public(url):
            # On to Scrapling's own handler (ad blocking), or the network.
            route.fallback()
            return
        if _is_main_frame_navigation(request):
            self.refused_navigations.append(url)
        logger.info("[webcrawler] aborted a request to a non-public host")
        route.abort("blockedbyclient")

    def _on_navigated(self, frame: Any) -> None:
        if getattr(frame, "parent_frame", None) is None:
            self.main_frame_urls.append(frame.url)

    def raise_if_refused(self) -> None:
        """After a failed load: was it this guard that stopped the page?"""
        if self.refused_navigations:
            raise RestrictedDestinationError(
                "the page tried to move to a host that is not publicly routable"
            )

    def check(self, final_url: str | None) -> None:
        """After the load: refuse a page that went through a non-public host."""
        self.raise_if_refused()
        for url in [*self.main_frame_urls, final_url]:
            if url and not self._public(url):
                raise RestrictedDestinationError(
                    "the page led to a host that is not publicly routable"
                )


def _is_main_frame_navigation(request: Any) -> bool:
    try:
        return bool(request.is_navigation_request()) and (
            request.frame.parent_frame is None
        )
    except Exception:
        # A service worker's request has no frame: not a navigation of the page.
        return False
