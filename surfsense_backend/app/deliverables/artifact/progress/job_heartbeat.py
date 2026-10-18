"""Persist a running artifact job's progress without touching the run's session.

The subagent's tools share the executor's session, so committing or rolling
it back from a callback would cut a tool's transaction in half. Each write
here opens its own short-lived session instead. A job that is no longer
running (cancelled, superseded) just stops receiving writes: stopping the run
is the cancellation watcher's job, not this one's.
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import Callable
from typing import Any

from app.deliverables.jobs.service import heartbeat_deliverable_job

from .phases import ProgressUpdate
from .throttle import HeartbeatThrottle

logger = logging.getLogger(__name__)


class JobProgressHeartbeat:
    def __init__(
        self,
        session_maker: Callable[[], Any],
        job_id: int,
        *,
        task_id: str | None,
        throttle: HeartbeatThrottle | None = None,
        clock: Callable[[], float] = time.monotonic,
        last_written: ProgressUpdate | None = None,
    ) -> None:
        """``last_written`` is what the caller already stored, so it is not repeated."""
        self._session_maker = session_maker
        self._job_id = job_id
        self._task_id = task_id
        self._throttle = throttle or HeartbeatThrottle()
        self._clock = clock
        if last_written is not None:
            self._throttle.written(last_written, clock())
        # Parallel tool calls report concurrently; one writer keeps the row monotonic.
        self._lock = asyncio.Lock()
        self._stopped = False

    async def __call__(self, update: ProgressUpdate) -> None:
        async with self._lock:
            if self._stopped:
                return
            now = self._clock()
            if not self._throttle.should_write(update, now):
                return
            # Recorded before the write so a failing database is retried at
            # the throttle's cadence rather than on every event.
            self._throttle.written(update, now)
            try:
                async with self._session_maker() as session:
                    updated = await heartbeat_deliverable_job(
                        session,
                        self._job_id,
                        phase=update.phase,
                        progress=update.progress,
                        task_id=self._task_id,
                    )
                    if updated is None:
                        await session.rollback()
                        self._stopped = True
                        return
                    await session.commit()
            except asyncio.CancelledError:
                raise
            except Exception:
                logger.warning(
                    "Could not record progress for deliverable job %s",
                    self._job_id,
                    exc_info=True,
                )
