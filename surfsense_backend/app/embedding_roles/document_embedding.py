"""Embed texts that will be stored: in batches sized for the server they go
to, each batch in one of that server's slots.

Where a batch goes:

- To the document model (``EMBEDDING_DOCUMENT_MODEL``). When that is another
  model than the question one, it is its own host: batches of
  ``EMBEDDING_DOCUMENT_BATCH_SIZE``, at most ``EMBEDDING_DOCUMENT_CONCURRENCY``
  of them in flight host-wide, and one call sends its batches side by side up
  to that cap. When both lines name one model, it is the question model's
  server, below.
- To the question model's server (``EMBEDDING_QUERY_MODEL``) when both lines
  name one model, and for a fallback batch: batches of
  ``EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE``, at most
  ``EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY`` at once, one after another, so
  a question there waits behind one short batch at most.

When the document model fails a batch (its host down, out of credit, slower
than ``EMBEDDING_DOCUMENT_TIMEOUT_SECONDS``, vectors of the wrong length), the
batch fails once (no text-by-text retry: ``clients``) and is embedded by the
question model instead, if that is a different client giving the agreed
length and ``EMBEDDING_DOCUMENT_FALLBACK`` is on; the breaker then sends every
batch there for ``EMBEDDING_DOCUMENT_BREAKER_SECONDS`` (``breaker``). The
document model's slot is given back before the question model's is taken.

A model loaded into this process takes no slot (each process has its own
copy) and embeds one text at a time under ``MODEL_LOCK``.
"""

from __future__ import annotations

import logging
import threading
from collections.abc import Iterator, Sequence
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any

import numpy as np
from celery.exceptions import SoftTimeLimitExceeded

from app.config import config
from app.embedding_roles import breaker
from app.embedding_roles.clients import embed_batch_once
from app.embedding_roles.document_slots import document_embedding_slot
from app.embedding_roles.settings import DEFAULT_DOCUMENT_CONCURRENCY, runs_in_process

logger = logging.getLogger(__name__)

# HuggingFace fast tokenizers (Rust-backed) are not thread-safe: concurrent
# use from several threads raises "RuntimeError: Already borrowed". This lock
# guards the tokenizer and a model loaded into this process, and nothing
# else: never a slot wait, never a request to a hosted model.
MODEL_LOCK = threading.RLock()

# With no host-wide cap (concurrency 0), one call still sends at most this
# many of its batches at once.
UNCAPPED_PARALLEL_BATCHES = DEFAULT_DOCUMENT_CONCURRENCY


@dataclass(frozen=True, slots=True)
class _Server:
    """Where a batch goes, and the limits there."""

    model: Any
    name: str | None
    slots: int
    batch_size: int

    @property
    def in_process(self) -> bool:
        return runs_in_process(self.name)


def embed_documents(texts: Sequence[str]) -> list[np.ndarray]:
    """Vectors for ``texts`` (already cut to the model's limit), in order."""
    texts = list(texts)
    if not texts:
        return []
    server = _document_server()
    fallback = _fallback_server()
    if fallback is not None and _breaker_open(server):
        return _embed_on(fallback, texts)
    batches = _split(texts, server.batch_size)
    parallel = 1 if server.in_process else server.slots or UNCAPPED_PARALLEL_BATCHES
    parallel = min(parallel, len(batches))
    if parallel <= 1:
        return [vector for batch in batches for vector in embed_document_batch(batch)]
    with ThreadPoolExecutor(
        max_workers=parallel, thread_name_prefix="document-embedding"
    ) as pool:
        answers = list(pool.map(embed_document_batch, batches))
    return [vector for answer in answers for vector in answer]


def embed_document_batch(texts: Sequence[str]) -> list[np.ndarray]:
    """One batch's vectors, from the document model or its fallback."""
    server = _document_server()
    fallback = _fallback_server()
    if fallback is not None and _breaker_open(server):
        return _embed_on(fallback, texts)
    try:
        with _slot(server):
            # Checked again once the slot is ours: a batch that waited for it
            # while another batch tripped the breaker must not go to the host
            # the breaker is keeping batches from. The slot is given back
            # (leaving the with) before the fallback below.
            if fallback is None or not _breaker_open(server):
                return _embed_checked(server, texts)
    except SoftTimeLimitExceeded:
        raise  # the task ran out of time, not the model
    except Exception as exc:
        if fallback is None:
            raise
        seconds = config.EMBEDDING_DOCUMENT_BREAKER_SECONDS
        logger.warning(
            "Document embedding model %s failed for a batch of %d (%s); "
            "embedding this batch with %s%s",
            server.name,
            len(texts),
            exc,
            fallback.name,
            f", and every batch for the next {seconds:.0f}s" if seconds > 0 else "",
        )
        breaker.trip(server.name, seconds, exc)
        return _embed_on(fallback, texts)
    return _embed_on(fallback, texts)


def _document_server() -> _Server:
    document = config.document_embedding_model_instance
    if document is config.query_embedding_model_instance:
        return _query_server()
    return _Server(
        model=document,
        name=config.EMBEDDING_DOCUMENT_MODEL,
        slots=config.EMBEDDING_DOCUMENT_CONCURRENCY,
        batch_size=config.EMBEDDING_DOCUMENT_BATCH_SIZE,
    )


def _query_server() -> _Server:
    return _Server(
        model=config.query_embedding_model_instance,
        name=config.EMBEDDING_QUERY_MODEL,
        slots=config.EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY,
        batch_size=config.EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE,
    )


def _fallback_server() -> _Server | None:
    if not config.EMBEDDING_DOCUMENT_FALLBACK:
        return None
    if (
        config.query_embedding_model_instance
        is config.document_embedding_model_instance
    ):
        return None
    return _query_server()


def _breaker_open(server: _Server) -> bool:
    if config.EMBEDDING_DOCUMENT_BREAKER_SECONDS <= 0:
        return False
    return breaker.is_open(server.name)


def _embed_on(server: _Server, texts: Sequence[str]) -> list[np.ndarray]:
    """``texts`` on ``server`` in its batch size, one batch after another."""
    vectors: list[np.ndarray] = []
    for batch in _split(list(texts), server.batch_size):
        with _slot(server):
            vectors.extend(_embed_checked(server, batch))
    return vectors


@contextmanager
def _slot(server: _Server) -> Iterator[None]:
    if server.in_process:  # its own copy in every process: nothing to share
        yield
        return
    with document_embedding_slot(server.name, server.slots):
        yield


def _embed_checked(server: _Server, texts: Sequence[str]) -> list[np.ndarray]:
    if server.in_process:
        # A model in this process embeds one text at a time: batching only
        # pads every text to the longest.
        with MODEL_LOCK:
            vectors = [server.model.embed(text) for text in texts]
    else:
        vectors = embed_batch_once(server.model, texts)
    _check(vectors, server.name, expected=len(texts))
    return vectors


def _split(texts: list[str], size: int) -> list[list[str]]:
    return [texts[start : start + size] for start in range(0, len(texts), size)]


def _check(vectors: list, name: str | None, *, expected: int) -> None:
    if len(vectors) != expected:
        raise RuntimeError(
            f"Embedding model {name} returned {len(vectors)} vectors for {expected} texts"
        )
    dimension = config.EMBEDDING_DIMENSION
    for vector in vectors:
        if len(vector) != dimension:
            raise RuntimeError(
                f"Embedding model {name} returned {len(vector)}-dimension vectors; "
                f"this server stores {dimension}"
            )
