"""The Redis connection the document slots and the breaker share.

Redis is on the same host, so a call that takes longer than
``REDIS_TIMEOUT_SECONDS`` means it is down. Neither the slots nor the breaker
are a reason to fail indexing: when Redis cannot be reached, documents are
embedded without a slot and the breaker is kept in this process only. Redis
is then left alone for ``REDIS_RETRY_SECONDS`` (and said so in the log once),
so a Redis that hangs costs one 5 s wait a minute per process, not two per
batch.
"""

from __future__ import annotations

import logging
import threading
import time

import redis
from redis.exceptions import RedisError

from app.config import config

logger = logging.getLogger(__name__)

# Redis is on the same host: a call that takes longer means it is down.
REDIS_TIMEOUT_SECONDS = 5.0
# After Redis fails, how long it is left alone before the next try; the
# warning is said at most this often too.
REDIS_RETRY_SECONDS = 60.0

_client: redis.Redis | None = None
_client_lock = threading.Lock()
_down_until = float("-inf")


class RedisUnavailableError(RedisError):
    """Redis failed less than ``REDIS_RETRY_SECONDS`` ago; not asked again yet."""


def redis_client() -> redis.Redis:
    """The shared client, or ``RedisUnavailableError`` while Redis is left alone."""
    global _client
    if time.monotonic() < _down_until:
        raise RedisUnavailableError("Redis failed less than a minute ago")
    with _client_lock:
        if _client is None:
            _client = redis.Redis.from_url(
                config.REDIS_APP_URL,
                decode_responses=True,
                socket_timeout=REDIS_TIMEOUT_SECONDS,
                socket_connect_timeout=REDIS_TIMEOUT_SECONDS,
            )
        return _client


def redis_failed(exc: RedisError, *, doing: str) -> None:
    """Leave Redis alone for a minute and say so, once."""
    global _down_until
    if isinstance(exc, RedisUnavailableError):
        return
    _down_until = time.monotonic() + REDIS_RETRY_SECONDS
    logger.warning(
        "Redis unavailable for %s (%s); document embedding goes on without the "
        "host-wide slots and with this process's own breaker, and tries Redis "
        "again in %.0fs",
        doing,
        exc,
        REDIS_RETRY_SECONDS,
    )
