"""A host-wide cap on document-embedding requests in flight at one server.

Documents are embedded by the backend and by every Celery worker process, and
each process only sees its own work, so the cap lives in Redis: a request
first takes one of the server's Redis-held slots (keys
``embedding:document_slot:<model>:<n>``) and gives it back when the model
answers. Each server the batches go to has its own slots: the document
model's own host (``EMBEDDING_DOCUMENT_CONCURRENCY``) and the question model's
server (``EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY``). Questions never take
one, so a question is never queued behind an import's batches. A model loaded
into this process takes none either (each process has its own copy).

A slot is never a reason to fail indexing: when Redis cannot be reached the
batch is embedded without one (``redis_link``), and when no slot comes free
within ``SLOT_WAIT_SECONDS`` the batch goes anyway; both are logged.
"""

from __future__ import annotations

import logging
import random
import threading
import time
from collections.abc import Iterator
from contextlib import contextmanager, suppress

import redis
from redis.exceptions import LockError, RedisError

from app.embedding_roles import redis_link

logger = logging.getLogger(__name__)

SLOT_KEY_PREFIX = "embedding:document_slot"
# A slot frees itself this long after its last renewal if its holder died (a
# worker killed mid-request), so a dead worker blocks a slot for a minute at
# most. A live holder renews it every SLOT_RENEW_SECONDS for as long as its
# request runs, however long the model takes.
SLOT_LEASE_SECONDS = 60.0
# Three renewals fit in one lease, so a Redis call that takes its whole 5 s
# timeout (or two) still leaves the lease standing.
SLOT_RENEW_SECONDS = 20.0
# How long a batch waits for a slot before it is embedded anyway. Slots turn
# over every second or so, so this is reached only when the model server has
# stopped answering for a long time; the batch's own request then fails too.
SLOT_WAIT_SECONDS = 900.0
# Between tries for a slot, plus up to half as much again at random so the
# waiting processes do not all ask Redis at the same instant: 0.10 to 0.15 s.
_POLL_SECONDS = 0.1


@contextmanager
def document_embedding_slot(
    server: str | None, slots: int, *, wait: float = SLOT_WAIT_SECONDS
) -> Iterator[None]:
    """Hold one of ``server``'s ``slots`` for the duration of the block."""
    if slots <= 0:  # 0: no cap
        yield
        return

    held = None
    try:
        held = _acquire(redis_link.redis_client(), server, slots, wait)
    except RedisError as exc:
        redis_link.redis_failed(exc, doing="a document embedding slot")
    else:
        if held is None:
            logger.warning(
                "No free document embedding slot for %s after %.0fs; embedding anyway",
                server,
                wait,
            )
    renewal = _Renewal(held) if held is not None else None
    try:
        yield
    finally:
        if renewal is not None:
            renewal.stop()
        if held is not None:
            with suppress(LockError, RedisError):
                held.release()


def slot_key(server: str | None, index: int) -> str:
    return f"{SLOT_KEY_PREFIX}:{server}:{index}"


def _acquire(client: redis.Redis, server: str | None, count: int, wait: float):
    """The first free slot, trying each without blocking until ``wait`` ends."""
    deadline = time.monotonic() + wait
    while True:
        for index in range(count):
            # Not thread-local: the renewal thread extends the lease.
            lock = client.lock(
                slot_key(server, index),
                timeout=SLOT_LEASE_SECONDS,
                thread_local=False,
            )
            if lock.acquire(blocking=False):
                return lock
        if time.monotonic() >= deadline:
            return None
        time.sleep(_POLL_SECONDS * (1 + random.random() / 2))


class _Renewal:
    """Resets a held slot's lease every ``SLOT_RENEW_SECONDS`` until stopped."""

    def __init__(self, lock) -> None:
        self._lock = lock
        self._stop = threading.Event()
        self._thread = threading.Thread(
            target=self._run, name="document-slot-lease", daemon=True
        )
        self._thread.start()

    def _run(self) -> None:
        while not self._stop.wait(SLOT_RENEW_SECONDS):
            try:
                self._lock.reacquire()
            except LockError:
                logger.warning(
                    "A document embedding slot's lease ran out during its request "
                    "(Redis unreachable for over %.0fs); another request may run "
                    "beside it",
                    SLOT_LEASE_SECONDS,
                )
                return
            except RedisError as exc:
                redis_link.redis_failed(exc, doing="renewing a document slot")

    def stop(self) -> None:
        self._stop.set()
        # A renewal under way finishes before the slot is given back.
        self._thread.join(timeout=redis_link.REDIS_TIMEOUT_SECONDS + 1)
