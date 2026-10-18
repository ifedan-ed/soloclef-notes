"""Polite HTTP for the scholarly APIs: spaced per host as each asks, retried when throttled.

NCBI allows 3 requests a second (10 with a key), arXiv asks for one every three
seconds, and the others are free services worth sparing. Requests to one host
are spaced across every search in this process; the spacing is kept with a
thread lock and timestamps rather than an asyncio lock, because the API and the
Celery import tasks run on different event loops.

``ponytail:`` spacing is per process. Several API workers each keep their own
pace; a shared (Redis) pacer is the upgrade if NCBI ever complains.
"""

from __future__ import annotations

import asyncio
import threading
import time
from collections.abc import Awaitable, Callable, Mapping
from urllib.parse import quote, urlencode, urlsplit

from app.config import config

from ..curl_http import Reply, curl_get

# Takes a full URL (query already encoded) and returns the body as text.
FetchText = Callable[[str], Awaitable[str]]
# Sends one GET; the seam tests replace.
Sender = Callable[[str], Awaitable[Reply]]

_RETRY_STATUSES = frozenset({406, 429, 500, 502, 503, 504})
_RETRIES = 2
_RETRY_BASE_S = 3.0


class FetchFailedError(Exception):
    """The service answered with an error, or could not be reached."""


class ThrottledError(FetchFailedError):
    """The service kept refusing after the retries: it is busy or rate-limiting."""


def min_interval_for(host: str) -> float:
    if host == "eutils.ncbi.nlm.nih.gov":
        return 0.11 if config.NCBI_API_KEY else 0.34
    if host == "export.arxiv.org":
        return 3.0
    if host == "api.biorxiv.org":
        return 0.5
    if host == "www.ebi.ac.uk":
        return 0.2
    return 0.0


class HostPacer:
    """Hands out send times at least ``min_interval_for(host)`` apart."""

    def __init__(self, interval: Callable[[str], float] = min_interval_for) -> None:
        self._interval = interval
        self._next: dict[str, float] = {}
        self._lock = threading.Lock()

    def reserve(self, host: str) -> float:
        """Seconds to wait before sending to ``host``; the slot is taken."""
        now = time.monotonic()
        with self._lock:
            start = max(now, self._next.get(host, 0.0))
            self._next[host] = start + self._interval(host)
        return start - now

    async def wait(self, host: str) -> None:
        delay = self.reserve(host)
        if delay > 0:
            await asyncio.sleep(delay)


_pacer = HostPacer()


def build_url(base: str, params: Mapping[str, object]) -> str:
    """``base?params`` with colons left literal (arXiv's field prefixes read better so)."""
    pairs = [(key, str(value)) for key, value in params.items() if value is not None]
    return f"{base}?{urlencode(pairs, quote_via=quote, safe=':,')}"


async def polite_get_text(url: str, *, send: Sender = curl_get) -> str:
    """GET ``url`` in its host's turn; retry throttling answers, raise on the rest."""
    host = urlsplit(url).hostname or ""
    last_status = None
    for attempt in range(_RETRIES + 1):
        await _pacer.wait(host)
        try:
            reply = await send(url)
        except Exception as exc:
            raise FetchFailedError(f"{host} unreachable: {exc!r}") from exc
        if reply.status not in _RETRY_STATUSES:
            if reply.status >= 400:
                raise FetchFailedError(f"{host} answered {reply.status}")
            return reply.text
        last_status = reply.status
        if attempt < _RETRIES:
            await asyncio.sleep(_retry_after(reply) or _RETRY_BASE_S * (attempt + 1))
    raise ThrottledError(f"{host} answered {last_status} after {_RETRIES + 1} tries")


def _retry_after(reply: Reply) -> float | None:
    try:
        return min(float(reply.headers.get("retry-after", "")), 30.0)
    except ValueError:
        return None
