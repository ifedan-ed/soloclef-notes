"""Beat entry for the overdue-run sweep (``runtime/overdue.py``): every minute.

Kept apart from the task module so ``app.celery_app`` can merge it without
importing the task (which imports ``app.celery_app``).
"""

from __future__ import annotations

from celery.schedules import crontab

TASK_NAME = "automation_runs_sweep_overdue"

BEAT_SCHEDULE = {
    "automation-runs-sweep-overdue": {
        "task": TASK_NAME,
        "schedule": crontab(minute="*"),
        # A tick nobody picked up within the minute is replaced by the next.
        "options": {"expires": 50},
    },
}
