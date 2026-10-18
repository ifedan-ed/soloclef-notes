"""The embedding settings read from the environment, checked once at startup.

An unreadable value stops the server with a message naming the setting, so a
typo never silently means the opposite (a fallback switched on, no cap).

A document batch goes to one of two servers, and each has its own cap and
batch size:

- the document model's own host, when ``EMBEDDING_DOCUMENT_MODEL`` names
  another model than ``EMBEDDING_QUERY_MODEL`` (a hosted copy of it, say):
  ``EMBEDDING_DOCUMENT_CONCURRENCY`` and ``EMBEDDING_DOCUMENT_BATCH_SIZE``;
- the question model's server, when both lines name one model, and for every
  fallback batch: ``EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY`` and
  ``EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE``, small, so a question there
  waits behind at most one short batch.
"""

from __future__ import annotations

import math
import os
from collections.abc import Mapping
from dataclasses import dataclass

EMBEDDING_MODEL_ENV = "EMBEDDING_MODEL"
QUERY_MODEL_ENV = "EMBEDDING_QUERY_MODEL"
DOCUMENT_MODEL_ENV = "EMBEDDING_DOCUMENT_MODEL"
DOCUMENT_CONCURRENCY_ENV = "EMBEDDING_DOCUMENT_CONCURRENCY"
DOCUMENT_BATCH_SIZE_ENV = "EMBEDDING_DOCUMENT_BATCH_SIZE"
DOCUMENT_TIMEOUT_ENV = "EMBEDDING_DOCUMENT_TIMEOUT_SECONDS"
DOCUMENT_BREAKER_ENV = "EMBEDDING_DOCUMENT_BREAKER_SECONDS"
DOCUMENT_FALLBACK_ENV = "EMBEDDING_DOCUMENT_FALLBACK"
QUERY_HOST_CONCURRENCY_ENV = "EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY"
QUERY_HOST_BATCH_SIZE_ENV = "EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE"

# --- The document model's own host (another model name than the question's).
# Requests in flight there at once, across the backend and every Celery worker
# process. A hosted model answers requests side by side: measured on
# 2026-10-12 through this project's LiteLLM, OpenRouter's bge-m3 took 0.9 to
# 2.0 s for 8 chunks and 1.4 to 1.6 s for 32, so a request is mostly its round
# trip, and four at once keep an import moving without flooding the provider.
DEFAULT_DOCUMENT_CONCURRENCY = 4
# Texts per request there: 32 chunks cost hardly more than 8 at that host
# (above), so 32 a request is about four times the texts a second.
DEFAULT_DOCUMENT_BATCH_SIZE = 32
# How long one request there may take before it counts as failed. The slowest
# request measured at OpenRouter took 2.0 s (8 chunks, a cold first request)
# and a document's own text of 5,840 tokens 0.95 s, so 20 s is ten times the
# slowest answer seen: a host that has stopped answering costs 20 s, not the
# client's default 60. (The LiteLLM alias local-bge-m3 gives up on the local
# TEI after the same 20 s.) Only a separate host gets it: a model that also
# embeds questions keeps its client's own timeout.
DEFAULT_DOCUMENT_TIMEOUT_SECONDS = 20.0
# After a document request fails, batches go straight to the question model
# for this long (when the fallback is on) instead of each trying the failed
# host first. Every process sees it (Redis). An outage (down, out of credit,
# rate-limited) lasts minutes, so a dead host costs one round of failed
# requests every five minutes rather than one per batch. 0 turns it off.
DEFAULT_DOCUMENT_BREAKER_SECONDS = 300.0

# --- The question model's server, for document batches (both lines name one
# model, or a fallback batch). One at a time: a model server on CPU (TEI)
# works through one batch at a time, so a second request would only wait in
# its queue, and a question arriving then would wait behind both.
DEFAULT_QUERY_HOST_CONCURRENCY = 1
# Texts per document request there. Measured on this project's CPU TEI
# (bge-m3, 10 cores) on 2026-10-12: 32 chunks took 8.4 s in an earlier run and
# 3.3 s in this one, 16 took 1.6 s, 8 took 0.9 to 1.0 s. At 8, a question that
# arrives behind a batch waits about a second; at 32, three to eight. A
# document's own text (the first in its batch, up to EMBEDDING_MAX_TOKENS)
# costs the most whatever the size: about 16 s for 5,800 tokens on that CPU.
DEFAULT_QUERY_HOST_BATCH_SIZE = 8

_TRUE = frozenset({"true", "1", "yes", "on"})
_FALSE = frozenset({"false", "0", "no", "off"})


@dataclass(frozen=True, slots=True)
class EmbeddingRoleSettings:
    query_model: str | None
    document_model: str | None
    document_concurrency: int
    document_batch_size: int
    document_timeout_seconds: float
    document_breaker_seconds: float
    document_fallback: bool
    query_host_concurrency: int
    query_host_batch_size: int

    @property
    def one_model(self) -> bool:
        """Both roles name the same model: one instance serves both."""
        return self.query_model == self.document_model

    @property
    def document_has_own_host(self) -> bool:
        """Documents go to a server questions do not use: another model name,
        not loaded into this process. Only that host gets the document
        timeout."""
        return not self.one_model and not runs_in_process(self.document_model)


def runs_in_process(model: str | None) -> bool:
    """A model named without a provider (``x://``) is loaded into this process."""
    return "://" not in (model or "")


def load_embedding_role_settings(
    environ: Mapping[str, str] | None = None,
) -> EmbeddingRoleSettings:
    environ = os.environ if environ is None else environ
    default_model = _text(environ, EMBEDDING_MODEL_ENV)
    return EmbeddingRoleSettings(
        query_model=_text(environ, QUERY_MODEL_ENV) or default_model,
        document_model=_text(environ, DOCUMENT_MODEL_ENV) or default_model,
        document_concurrency=_whole_number(
            environ, DOCUMENT_CONCURRENCY_ENV, DEFAULT_DOCUMENT_CONCURRENCY, minimum=0
        ),
        document_batch_size=_whole_number(
            environ, DOCUMENT_BATCH_SIZE_ENV, DEFAULT_DOCUMENT_BATCH_SIZE, minimum=1
        ),
        document_timeout_seconds=_seconds(
            environ,
            DOCUMENT_TIMEOUT_ENV,
            DEFAULT_DOCUMENT_TIMEOUT_SECONDS,
            allow_zero=False,
        ),
        document_breaker_seconds=_seconds(
            environ,
            DOCUMENT_BREAKER_ENV,
            DEFAULT_DOCUMENT_BREAKER_SECONDS,
            allow_zero=True,
        ),
        document_fallback=_switch(environ, DOCUMENT_FALLBACK_ENV, default=True),
        query_host_concurrency=_whole_number(
            environ,
            QUERY_HOST_CONCURRENCY_ENV,
            DEFAULT_QUERY_HOST_CONCURRENCY,
            minimum=0,
        ),
        query_host_batch_size=_whole_number(
            environ,
            QUERY_HOST_BATCH_SIZE_ENV,
            DEFAULT_QUERY_HOST_BATCH_SIZE,
            minimum=1,
        ),
    )


def _text(environ: Mapping[str, str], name: str) -> str | None:
    value = (environ.get(name) or "").strip()
    return value or None


def _whole_number(
    environ: Mapping[str, str], name: str, default: int, *, minimum: int
) -> int:
    raw = _text(environ, name)
    if raw is None:
        return default
    try:
        value = int(raw)
    except ValueError:
        value = None
    if value is None or value < minimum:
        raise ValueError(
            f"{name}={raw!r} is not a whole number of at least {minimum}; "
            f"unset, it is {default}"
        )
    return value


def _seconds(
    environ: Mapping[str, str], name: str, default: float, *, allow_zero: bool
) -> float:
    raw = _text(environ, name)
    if raw is None:
        return default
    try:
        value = float(raw)
    except ValueError:
        value = math.nan
    if not math.isfinite(value) or value < 0 or (value == 0 and not allow_zero):
        least = "0 or more" if allow_zero else "more than 0"
        raise ValueError(
            f"{name}={raw!r} is not a number of seconds, {least}; "
            f"unset, it is {default:g}"
        )
    return value


def _switch(environ: Mapping[str, str], name: str, *, default: bool) -> bool:
    raw = _text(environ, name)
    if raw is None:
        return default
    if raw.lower() in _TRUE:
        return True
    if raw.lower() in _FALSE:
        return False
    raise ValueError(
        f"{name}={raw!r} is neither TRUE nor FALSE; unset, it is "
        f"{'TRUE' if default else 'FALSE'}"
    )
