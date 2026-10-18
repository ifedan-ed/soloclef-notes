"""Record a search of several sources in the runs log: one row per source.

Each source is billed on its own, so each gets its own ``research.<source>``
row with its own status, results and cost, exactly like a search of that source
alone. The input also names every source the search asked, so the rows of one
search can be told apart from separate searches.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import AsyncIterator, Callable
from typing import Any
from uuid import UUID

from .run_log import (
    CANCELLED,
    RUN_ORIGIN,
    RunRecorder,
    _detached,
    _record_quietly,
    serialized_rows,
)
from .schemas import ResearchSource, SearchRequest


async def logged_multi_research_events(
    events: AsyncIterator[dict],
    *,
    request: SearchRequest,
    record: RunRecorder,
    workspace_id: int,
    user_id: UUID | None,
    notebook_id: int,
    cost_micros_for: Callable[[ResearchSource], int],
) -> AsyncIterator[dict]:
    """Pass the events through, then record how each source ended.

    A source that answered is a success once the search finishes, with the
    merged results it contributed; one that failed is an error with its reason;
    a search stopped midway records the sources still out (or not yet merged)
    as cancelled.
    """
    started = time.perf_counter()
    sources = request.selected
    outcome: dict[str, dict[str, Any]] = {}
    rows: list[dict[str, Any]] = []
    progress: list[dict[str, Any]] = []
    finished = done = False
    try:
        async for event in events:
            kind = event.get("type")
            if kind == "phase":
                progress.append(
                    {"phase": event.get("phase"), "progress": event.get("progress")}
                )
            elif kind == "provider" and event.get("status") in ("done", "error"):
                outcome[event["source"]] = event
            elif kind == "results":
                rows = list(event.get("results") or [])
            elif kind == "done":
                done = True
            yield event
        finished = True
    finally:
        duration_ms = int((time.perf_counter() - started) * 1000)
        runs = []
        for source in sources:
            answered = outcome.get(source, {})
            if answered.get("status") == "error":
                status, error = "error", answered.get("message")
            elif answered.get("status") == "done" and done:
                status, error = "success", None
            else:
                status, error = "cancelled", CANCELLED
            runs.append(
                {
                    "workspace_id": workspace_id,
                    "capability": f"research.{source}",
                    "origin": RUN_ORIGIN,
                    "status": status,
                    "serialized": serialized_rows(
                        [row for row in rows if row.get("source") == source]
                    ),
                    "input": {
                        "query": request.query,
                        "source": source,
                        "sources": list(sources),
                        "mode": request.mode,
                        "notebook_id": notebook_id,
                    },
                    "user_id": user_id,
                    "error": error,
                    "duration_ms": duration_ms,
                    "cost_micros": cost_micros_for(source),
                    "progress": progress,
                }
            )
        if finished:
            for kwargs in runs:
                await _record_quietly(record, kwargs)
        else:
            loop = asyncio.get_running_loop()
            for kwargs in runs:
                task = loop.create_task(_record_quietly(record, kwargs))
                _detached.add(task)
                task.add_done_callback(_detached.discard)
