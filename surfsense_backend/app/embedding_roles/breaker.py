"""After the document model fails, send document batches straight to the
question model for a while.

Without it, every batch of an import would first wait out the failed host
(up to ``EMBEDDING_DOCUMENT_TIMEOUT_SECONDS`` each when it hangs) before its
fallback. One failure opens the breaker for ``EMBEDDING_DOCUMENT_BREAKER_SECONDS``
for every process: a Redis key (``embedding:document_breaker:<model>``) that
expires by itself, after which the next batch tries the document model again.
Deleting the key closes it early. While Redis cannot be reached, each process
keeps its own breaker instead.

It is only consulted when there is a fallback to go to (two models,
``EMBEDDING_DOCUMENT_FALLBACK`` on); otherwise every batch tries the document
model, since there is nowhere else to send it.
"""

from __future__ import annotations

import threading
import time

from redis.exceptions import RedisError

from app.embedding_roles import redis_link

BREAKER_KEY_PREFIX = "embedding:document_breaker"

# This process's own copy, used while Redis cannot be reached:
# model name -> monotonic time the breaker closes.
_local_open_until: dict[str, float] = {}
_local_lock = threading.Lock()


def breaker_key(model: str | None) -> str:
    return f"{BREAKER_KEY_PREFIX}:{model}"


def is_open(model: str | None) -> bool:
    """True while batches for ``model`` should skip it."""
    try:
        return bool(redis_link.redis_client().exists(breaker_key(model)))
    except RedisError as exc:
        redis_link.redis_failed(exc, doing="the document breaker")
    with _local_lock:
        return _local_open_until.get(str(model), float("-inf")) > time.monotonic()


def trip(model: str | None, seconds: float, reason: BaseException) -> None:
    """Open the breaker for ``model`` for ``seconds`` (nothing when 0)."""
    if seconds <= 0:
        return
    with _local_lock:
        _local_open_until[str(model)] = time.monotonic() + seconds
    try:
        redis_link.redis_client().set(
            breaker_key(model), str(reason)[:500], px=int(seconds * 1000)
        )
    except RedisError as exc:
        redis_link.redis_failed(exc, doing="the document breaker")
