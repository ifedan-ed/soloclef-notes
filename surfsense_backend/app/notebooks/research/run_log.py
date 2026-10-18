"""Record each notebook research search in the runs log, beside the scraper runs.

The runs table (User settings -> API Access) is where a user checks what the
scrapers did and what they cost. A research search spends the same scrapers
from inside a notebook, so it lands there too: one ``research.<source>`` row per
search, with its query, mode, result count, duration, status and cost.
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from collections.abc import AsyncIterator, Awaitable, Callable
from typing import Any
from uuid import UUID

from app.capabilities.core.runs import SerializedOutput

from .schemas import SearchRequest

logger = logging.getLogger(__name__)

RUN_ORIGIN = "notebook"
CANCELLED = "Cancelled before the search finished."

# Takes record_run's keyword arguments; returns the run id or None.
RunRecorder = Callable[..., Awaitable[str | None]]

# Recordings started after a disconnect run on without the request; hold them
# until they finish so the event loop does not drop them half-written.
_detached: set[asyncio.Task] = set()


def capability_name(request: SearchRequest) -> str:
    return f"research.{request.source}"


def serialized_rows(rows: list[dict[str, Any]]) -> SerializedOutput:
    """The result rows as the runs log stores outputs: one JSON object per line."""
    text = "\n".join(json.dumps(row, default=str, ensure_ascii=False) for row in rows)
    return SerializedOutput(text=text, item_count=len(rows), char_count=len(text))


async def logged_research_events(
    events: AsyncIterator[dict],
    *,
    request: SearchRequest,
    record: RunRecorder,
    workspace_id: int,
    user_id: UUID | None,
    notebook_id: int,
    cost_micros: Callable[[], int],
) -> AsyncIterator[dict]:
    """Pass the search's events through, then record how the search ended.

    A search that stops before ``done`` (the user cancelled, the tab closed) is
    recorded as cancelled; one that reports an error is recorded with it.
    """
    started = time.perf_counter()
    rows: list[dict[str, Any]] = []
    progress: list[dict[str, Any]] = []
    status, error = "error", None
    finished = False
    try:
        async for event in events:
            kind = event.get("type")
            if kind == "phase":
                progress.append(
                    {"phase": event.get("phase"), "progress": event.get("progress")}
                )
            elif kind == "results":
                rows = list(event.get("results") or [])
            elif kind == "error":
                error = event.get("message")
            elif kind == "done":
                status = "success"
            yield event
        finished = True
    finally:
        if not finished and status != "success":
            status, error = "cancelled", CANCELLED
        kwargs = {
            "workspace_id": workspace_id,
            "capability": capability_name(request),
            "origin": RUN_ORIGIN,
            "status": status,
            "serialized": serialized_rows(rows),
            "input": {
                "query": request.query,
                "source": request.source,
                "mode": request.mode,
                "notebook_id": notebook_id,
            },
            "user_id": user_id,
            "error": error,
            "duration_ms": int((time.perf_counter() - started) * 1000),
            "cost_micros": cost_micros(),
            "progress": progress,
        }
        if finished:
            await _record_quietly(record, kwargs)
        else:
            task = asyncio.get_running_loop().create_task(
                _record_quietly(record, kwargs)
            )
            _detached.add(task)
            task.add_done_callback(_detached.discard)


async def _record_quietly(record: RunRecorder, kwargs: dict[str, Any]) -> None:
    try:
        await record(**kwargs)
    except Exception:
        logger.exception("recording research run %s failed", kwargs["capability"])
