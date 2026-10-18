"""Carry the log context into Celery tasks, and use our format in Celery.

* ``setup_logging``: having a receiver makes Celery skip its own logging
  setup; we install ours instead (worker and beat).
* ``before_task_publish``: the publishing request's id (and user/workspace)
  ride along in the message headers.
* ``task_prerun`` / ``task_postrun``: each task runs inside a fresh context
  with the task name, a short task id and whatever the publisher sent.
"""

from __future__ import annotations

import contextlib
import sys
from typing import Any

from celery.signals import (
    before_task_publish,
    setup_logging,
    task_postrun,
    task_prerun,
)

from .context import end_log_context, get_log_context, start_log_context
from .setup import configure_logging

_HEADER = "surfsense_log_context"
_PROPAGATED = ("request_id", "user_id", "workspace_id", "thread_id", "job_id")
_tokens: dict[str, Any] = {}


def _service_from_argv() -> str:
    return "beat" if "beat" in sys.argv else "worker"


@setup_logging.connect
def _setup_logging(**_kwargs: Any) -> None:
    configure_logging(_service_from_argv())


@before_task_publish.connect
def _propagate_context(headers: dict | None = None, **_kwargs: Any) -> None:
    if headers is None:
        return
    ctx = get_log_context()
    carried = {k: ctx[k] for k in _PROPAGATED if ctx.get(k)}
    if carried:
        with contextlib.suppress(Exception):
            headers[_HEADER] = carried


@task_prerun.connect
def _enter_task_context(
    task_id: str | None = None, task: Any = None, **_kwargs: Any
) -> None:
    carried: dict[str, str] = {}
    with contextlib.suppress(Exception):
        request = getattr(task, "request", None)
        raw = getattr(request, _HEADER, None)
        if raw is None:
            raw = (getattr(request, "headers", None) or {}).get(_HEADER)
        if isinstance(raw, dict):
            carried = {k: str(v) for k, v in raw.items() if k in _PROPAGATED}
    token = start_log_context(
        task=getattr(task, "name", None),
        task_id=(task_id or "")[:8] or None,
        **carried,
    )
    if task_id:
        _tokens[task_id] = token


@task_postrun.connect
def _exit_task_context(task_id: str | None = None, **_kwargs: Any) -> None:
    token = _tokens.pop(task_id, None) if task_id else None
    if token is not None:
        end_log_context(token)
