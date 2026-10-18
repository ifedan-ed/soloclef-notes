"""Two recording embedding clients in place of the configured ones, and a
Redis kept in memory.

``roles`` installs a question model and a document model (two hosts of one
4-dimension model, both ``litellm://``) on the live ``config``, so a test
can call any public embedding path and see which client got the text. It
also installs ``FakeRedis`` for the document slots and the breaker, so no
test reaches a real Redis, and every test starts with the breaker closed.
"""

from __future__ import annotations

import threading
import time
from dataclasses import dataclass

import numpy as np
import pytest

from app.config import config
from app.embedding_roles import breaker, redis_link

DIMENSION = 4
QUERY_NAME = "litellm://openai/question-host"
DOCUMENT_NAME = "litellm://openai/document-host"


class RecordingModel:
    """An embedding client that records each request's texts."""

    def __init__(self, name: str, *, dimension: int = DIMENSION) -> None:
        self.name = name
        self.dimension = dimension
        self.requests: list[list[str]] = []
        self.fail_next = 0
        self.delay = 0.0
        self.in_flight = 0
        self.peak = 0
        self._lock = threading.Lock()

    def _answer(self, texts: list[str]) -> list[np.ndarray]:
        with self._lock:
            self.requests.append(list(texts))
            self.in_flight += 1
            self.peak = max(self.peak, self.in_flight)
            failing = self.fail_next > 0
            if failing:
                self.fail_next -= 1
        try:
            if self.delay:
                time.sleep(self.delay)
            if failing:
                raise RuntimeError(f"{self.name} is down")
        finally:
            with self._lock:
                self.in_flight -= 1
        return [np.full(self.dimension, len(t), dtype=np.float32) for t in texts]

    def embed(self, text: str) -> np.ndarray:
        return self._answer([text])[0]

    def embed_batch(self, texts: list[str]) -> list[np.ndarray]:
        return self._answer(texts)

    @property
    def texts(self) -> list[str]:
        return [t for request in self.requests for t in request]


class FakeRedis:
    """What the slots and the breaker ask of Redis: SET NX locks with a
    lease, EXISTS and SET PX, shared by every 'process' (thread) in a test."""

    def __init__(self) -> None:
        self.guard = threading.Lock()
        self.expires: dict[str, float] = {}  # key -> monotonic expiry
        self.values: dict[str, str] = {}
        self.peak: dict[str, int] = {}  # slot pool -> most held at once
        self.renewals = 0
        self.leases: list[float] = []

    def _alive(self, key: str) -> bool:
        expiry = self.expires.get(key)
        if expiry is None:
            return False
        if expiry <= time.monotonic():
            del self.expires[key]
            self.values.pop(key, None)
            return False
        return True

    def hold(self, key: str, seconds: float = 3600.0) -> None:
        """Another process holds ``key`` (a slot, the breaker)."""
        with self.guard:
            self.expires[key] = time.monotonic() + seconds

    def free(self, key: str) -> None:
        with self.guard:
            self.expires.pop(key, None)

    def held(self, prefix: str = "") -> set[str]:
        with self.guard:
            return {
                k for k in list(self.expires) if k.startswith(prefix) and self._alive(k)
            }

    def lock(self, key, timeout=None, thread_local=True):
        assert thread_local is False, "the renewal thread must see the token"
        self.leases.append(timeout)
        return _FakeLock(self, key, timeout)

    def exists(self, key) -> int:
        with self.guard:
            return int(self._alive(key))

    def set(self, key, value, px=None):
        with self.guard:
            self.values[key] = value
            self.expires[key] = time.monotonic() + (px / 1000 if px else 3600.0)
        return True


class _FakeLock:
    def __init__(self, redis: FakeRedis, key: str, timeout: float) -> None:
        self.redis, self.key, self.timeout = redis, key, timeout

    def acquire(self, blocking: bool = True) -> bool:
        r = self.redis
        with r.guard:
            if r._alive(self.key):
                return False
            r.expires[self.key] = time.monotonic() + self.timeout
            pool = self.key.rsplit(":", 1)[0]
            held = sum(1 for k in r.expires if k.startswith(pool + ":") and r._alive(k))
            r.peak[pool] = max(r.peak.get(pool, 0), held)
            return True

    def reacquire(self) -> bool:
        r = self.redis
        with r.guard:
            r.renewals += 1
            r.expires[self.key] = time.monotonic() + self.timeout
        return True

    def release(self) -> None:
        self.redis.free(self.key)


@dataclass
class Roles:
    query: RecordingModel
    document: RecordingModel
    redis: FakeRedis


@pytest.fixture
def roles(monkeypatch) -> Roles:
    query = RecordingModel("question-host")
    document = RecordingModel("document-host")
    redis = FakeRedis()
    settings = {
        "query_embedding_model_instance": query,
        "document_embedding_model_instance": document,
        "embedding_model_instance": document,
        "EMBEDDING_QUERY_MODEL": QUERY_NAME,
        "EMBEDDING_DOCUMENT_MODEL": DOCUMENT_NAME,
        "EMBEDDING_DIMENSION": DIMENSION,
        # One at a time on each server, so a test reads the requests in order;
        # the cap tests raise it.
        "EMBEDDING_DOCUMENT_CONCURRENCY": 1,
        "EMBEDDING_DOCUMENT_BATCH_SIZE": 32,
        "EMBEDDING_DOCUMENT_TIMEOUT_SECONDS": 20.0,
        "EMBEDDING_DOCUMENT_BREAKER_SECONDS": 300.0,
        "EMBEDDING_DOCUMENT_FALLBACK": True,
        "EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY": 1,
        "EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE": 8,
    }
    for name, value in settings.items():
        monkeypatch.setattr(config, name, value)
    monkeypatch.setattr(redis_link, "redis_client", lambda: redis)
    monkeypatch.setattr(redis_link, "_down_until", float("-inf"))
    monkeypatch.setattr(breaker, "_local_open_until", {})
    monkeypatch.setenv("EMBEDDING_MAX_TOKENS", "6000")
    return Roles(query=query, document=document, redis=redis)


class _EmptyResult:
    def all(self):
        return []

    def scalars(self):
        return self

    def unique(self):
        return self

    def scalar(self):
        return 0

    def first(self):
        return None

    def fetchall(self):
        return []

    def mappings(self):
        return self

    def __iter__(self):
        return iter(())


class NoRowsSession:
    """An AsyncSession stand-in whose every query matches nothing."""

    async def execute(self, *_args, **_kwargs):
        return _EmptyResult()

    async def scalars(self, *_args, **_kwargs):
        return _EmptyResult()

    async def scalar(self, *_args, **_kwargs):
        return None


@pytest.fixture
def no_rows_session() -> NoRowsSession:
    return NoRowsSession()
