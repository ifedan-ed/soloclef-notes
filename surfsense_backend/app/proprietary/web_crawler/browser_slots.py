# SurfSense proprietary crawler engine.
#
# Part of the ``app.proprietary`` package; licensed separately from the
# Apache-2.0 project root (see ``app/proprietary/LICENSE``).
"""How many full browsers the crawler runs at once in this process.

Each browser-tier fetch (DynamicFetcher, StealthyFetcher) starts its own
Chromium, a few hundred MB each. Start URLs are now fetched side by side
(``start_urls.py``) and up to three sub-agents can crawl at once, so without
a bound a busy turn could start a dozen browsers in the backend container.
Extra browser fetches wait for a free slot; nothing is dropped.

``WEB_CRAWL_BROWSER_CONCURRENCY`` (default 3, the most that ran at once
before start URLs went parallel) changes it. Documented in this package's
README ("Multi-page crawls").

The fetches run in worker threads (``asyncio.to_thread``), so this is a
thread semaphore: a waiting fetch blocks its worker thread, never the event
loop, and it works across the separate event loops Celery tasks use.
"""

from __future__ import annotations

import contextlib
import os
import threading
from collections.abc import Iterator


def _read_slots() -> int:
    try:
        value = int(os.environ.get("WEB_CRAWL_BROWSER_CONCURRENCY", "3"))
    except ValueError:
        return 3
    return value if value > 0 else 3


BROWSER_CONCURRENCY = _read_slots()

_slots = threading.BoundedSemaphore(BROWSER_CONCURRENCY)


@contextlib.contextmanager
def browser_slot() -> Iterator[None]:
    """Hold one browser slot for the duration of a browser-tier fetch."""
    with _slots:
        yield
