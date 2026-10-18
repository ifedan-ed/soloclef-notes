"""Hand a run to a worker, with a kill limit set safely past the run's own.

The executor stops a run itself when ``execution.timeout_seconds`` runs out,
records it as "Timed out" and sends the result email. Celery's limits are only
a backstop for a worker too wedged to do that, so they sit
``WORKER_KILL_GRACE_SECONDS`` later: a hard kill never beats the clean stop.
The soft limit is set to the same moment so Celery's global soft limit
(``task_soft_time_limit`` in ``app/celery_app.py``) never interrupts a long
run's event loop first.
"""

from __future__ import annotations

# Seconds between the run's own time limit and the worker being killed.
WORKER_KILL_GRACE_SECONDS = 120
# Seconds past its own limit after which a run still Running has surely lost
# its worker: the backstop kill above, plus a minute of slack. The overdue
# sweep times such a run out (``runtime/overdue.py``); the queue hand-off
# counts it as having stopped holding the queue by then
# (``concurrency/handoff.py``).
OVERDUE_AFTER_SECONDS = WORKER_KILL_GRACE_SECONDS + 60


def worker_time_limit(timeout_seconds: int) -> int:
    """Celery's hard (and soft) limit for a run allowed ``timeout_seconds``."""
    return max(1, int(timeout_seconds)) + WORKER_KILL_GRACE_SECONDS


def enqueue_run(run_id: int, *, timeout_seconds: int) -> None:
    """Queue ``automation_run_execute`` for ``run_id``.

    Safe to call more than once for the same run: the executor claims a run
    atomically, so a duplicate message finds it already started and exits.
    """
    # Imported here: the task module imports the executor, which hands off
    # queued runs through this function.
    from app.automations.tasks.execute_run import automation_run_execute

    limit = worker_time_limit(timeout_seconds)
    automation_run_execute.apply_async(
        args=[run_id], time_limit=limit, soft_time_limit=limit
    )
