"""Per-request / per-task log context (request id, user, workspace, chat turn).

The context is a *mutable dict* held in a ContextVar. The request middleware
(or a Celery ``task_prerun`` hook) installs a fresh dict; anything deeper in
the call stack enriches it with :func:`bind_log_context`. Because the dict
itself is shared, fields bound inside a dependency, a background task spawned
from the request, or an async generator still show on the request's later log
lines — even though each of those runs in a *copy* of the context.
"""

from __future__ import annotations

import contextlib
from collections.abc import Iterator
from contextvars import ContextVar
from typing import Any

_LOG_CONTEXT: ContextVar[dict[str, str] | None] = ContextVar(
    "surfsense_log_context", default=None
)

# Printed in this order by the text formatter; anything else follows.
CONTEXT_KEYS = (
    "request_id",
    "task",
    "task_id",
    "user_id",
    "workspace_id",
    "thread_id",
    "turn_id",
    "job_id",
)

_MAX_VALUE_LEN = 80


def _clean(value: Any) -> str:
    text = str(value)
    # Context values end up inside every log line; keep them single-line and
    # short so a hostile value (e.g. a client request id) cannot forge lines.
    text = "".join(ch if ch.isprintable() and ch != " " else "_" for ch in text)
    return text[:_MAX_VALUE_LEN]


def get_log_context() -> dict[str, str]:
    """Return a snapshot of the current context (never the live dict)."""
    ctx = _LOG_CONTEXT.get()
    return dict(ctx) if ctx else {}


def bind_log_context(**fields: Any) -> None:
    """Add fields to the current log context; ``None`` values are skipped."""
    clean = {k: _clean(v) for k, v in fields.items() if v is not None and v != ""}
    if not clean:
        return
    ctx = _LOG_CONTEXT.get()
    if ctx is None:
        _LOG_CONTEXT.set(clean)
    else:
        ctx.update(clean)


@contextlib.contextmanager
def log_context(**fields: Any) -> Iterator[dict[str, str]]:
    """Install a *fresh* context for the duration of the block."""
    token = _LOG_CONTEXT.set({})
    try:
        bind_log_context(**fields)
        yield _LOG_CONTEXT.get() or {}
    finally:
        _LOG_CONTEXT.reset(token)


def start_log_context(**fields: Any) -> Any:
    """Install a fresh context and return the token for :func:`end_log_context`.

    For hook pairs that cannot share a ``with`` block (Celery prerun/postrun).
    """
    token = _LOG_CONTEXT.set({})
    bind_log_context(**fields)
    return token


def end_log_context(token: Any) -> None:
    try:
        _LOG_CONTEXT.reset(token)
    except (ValueError, RuntimeError):
        # The token belongs to another context (hooks ran in different
        # contexts); clearing is the safe fallback so fields cannot leak
        # into the next task on this worker.
        _LOG_CONTEXT.set(None)
