"""Celery task that runs one automation. Thin wrapper over ``runtime.executor``.

Enqueue it through ``app.automations.dispatch.enqueue.enqueue_run``, which
sets the worker's kill limit safely past the run's own time limit.
"""

from __future__ import annotations

import logging

from app.automations.runtime.executor import execute_run
from app.automations.runtime.stop_probe import run_ended_elsewhere
from app.celery_app import celery_app
from app.tasks.celery_tasks import (
    get_celery_session_maker,
    run_async_celery_task,
)

logger = logging.getLogger(__name__)

TASK_NAME = "automation_run_execute"


@celery_app.task(name=TASK_NAME, bind=True)
def automation_run_execute(self, run_id: int) -> None:
    """Execute one ``AutomationRun``. Idempotent: a run already claimed no-ops."""
    return run_async_celery_task(lambda: _impl(run_id))


async def _impl(run_id: int) -> None:
    session_maker = get_celery_session_maker()
    async with session_maker() as session:
        try:
            await execute_run(
                session,
                run_id,
                should_stop=run_ended_elsewhere(session_maker, run_id),
            )
        except Exception:
            logger.exception("automation_run %d failed unexpectedly", run_id)
            await session.rollback()
            raise
