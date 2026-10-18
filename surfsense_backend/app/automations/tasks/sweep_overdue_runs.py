"""Celery task for the overdue-run sweep. Thin wrapper over ``runtime.overdue``."""

from __future__ import annotations

import logging
from datetime import UTC, datetime

from app.automations.runtime.overdue import (
    restart_stalled_queues,
    time_out_overdue_runs,
)
from app.celery_app import celery_app
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

from .sweep_overdue_runs_beat import TASK_NAME

logger = logging.getLogger(__name__)


@celery_app.task(name=TASK_NAME)
def automation_runs_sweep_overdue() -> None:
    """Time out runs whose worker is gone; restart queues that stalled."""
    return run_async_celery_task(_sweep)


async def _sweep() -> None:
    """Run both passes, each on its own session.

    One pass failing (a database error, an unexpected row) is logged and does
    not stop the other: lost runs still get timed out when restarting a queue
    fails, and the other way round. Both are tried again a minute later.
    """
    session_maker = get_celery_session_maker()
    now = datetime.now(UTC)
    for sweep_pass in (time_out_overdue_runs, restart_stalled_queues):
        try:
            async with session_maker() as session:
                await sweep_pass(session, now=now)
        except Exception:
            logger.exception("automation runs sweep: %s failed", sweep_pass.__name__)
