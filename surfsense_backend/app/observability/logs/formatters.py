"""One line format for every SurfSense process (API, Celery worker, beat).

Text (default, readable in ``docker logs``)::

    2026-09-25T01:30:29.778Z INFO  app.tasks.chat [req=req_c4eb user=1a2b3c4d ws=3 thread=86] message

JSON (``LOG_FORMAT=json``), one object per line, for shipping to a log store.
Both are UTC and both are passed through :func:`redact`.
"""

from __future__ import annotations

import json
import logging
import time
from typing import Any

from .context import CONTEXT_KEYS, get_log_context
from .redaction import redact

_SHORT_KEYS = {
    "request_id": "req",
    "task": "task",
    "task_id": "task_id",
    "user_id": "user",
    "workspace_id": "ws",
    "thread_id": "thread",
    "turn_id": "turn",
    "job_id": "job",
}

# Logger-name prefixes that read better shortened in the text format.
_NAME_ALIASES = (
    ("surfsense.perf", "perf"),
    ("app.agents.chat.multi_agent_chat.", "agent."),
    ("app.tasks.chat.streaming.", "chat."),
)


def _utc_iso(created: float) -> str:
    secs = int(created)
    millis = int((created - secs) * 1000)
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(secs)) + f".{millis:03d}Z"


def _context_for(record: logging.LogRecord) -> dict[str, str]:
    # A record may carry its own context (e.g. set by a filter in another
    # thread); otherwise read the live one.
    ctx = getattr(record, "log_context", None)
    return ctx if isinstance(ctx, dict) else get_log_context()


def _short_name(name: str) -> str:
    for prefix, alias in _NAME_ALIASES:
        if name.startswith(prefix):
            return alias + name[len(prefix) :]
    return name


class TextFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        message = record.getMessage().rstrip()
        ctx = _context_for(record)
        parts = []
        for key in CONTEXT_KEYS:
            value = ctx.get(key)
            if not value:
                continue
            if key == "user_id":
                value = value[:8]
            parts.append(f"{_SHORT_KEYS[key]}={value}")
        for key, value in ctx.items():
            if key not in _SHORT_KEYS and value:
                parts.append(f"{key}={value}")
        trace_id = getattr(record, "otelTraceID", "0")
        if trace_id and trace_id != "0":
            parts.append(f"trace={trace_id}")
        context = f" [{' '.join(parts)}]" if parts else ""
        line = (
            f"{_utc_iso(record.created)} {record.levelname:<5} "
            f"{_short_name(record.name)}{context} {message}"
        )
        if record.exc_info and not record.exc_text:
            record.exc_text = self.formatException(record.exc_info)
        if record.exc_text:
            line = f"{line}\n{record.exc_text}"
        if record.stack_info:
            line = f"{line}\n{self.formatStack(record.stack_info)}"
        return redact(line)


class JsonFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, Any] = {
            "ts": _utc_iso(record.created),
            "level": record.levelname,
            "logger": record.name,
            "msg": redact(record.getMessage()),
        }
        payload.update(_context_for(record))
        trace_id = getattr(record, "otelTraceID", "0")
        if trace_id and trace_id != "0":
            payload["trace_id"] = trace_id
            payload["span_id"] = getattr(record, "otelSpanID", "0")
        service = getattr(record, "service", None)
        if service:
            payload["service"] = service
        if record.exc_info:
            payload["exc"] = redact(self.formatException(record.exc_info))
        elif record.exc_text:
            payload["exc"] = redact(record.exc_text)
        return json.dumps(payload, ensure_ascii=False, default=str)
