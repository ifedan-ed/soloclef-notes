"""A run is handed to the worker with a kill limit past its own time limit."""

from __future__ import annotations

import pytest

from app.automations.dispatch import enqueue
from app.automations.tasks.execute_run import automation_run_execute

pytestmark = pytest.mark.unit


def test_worker_limits_sit_past_the_runs_own_limit(monkeypatch):
    calls = []
    monkeypatch.setattr(
        automation_run_execute, "apply_async", lambda **kw: calls.append(kw)
    )

    enqueue.enqueue_run(7, timeout_seconds=600)

    assert calls == [
        {
            "args": [7],
            "time_limit": 600 + enqueue.WORKER_KILL_GRACE_SECONDS,
            "soft_time_limit": 600 + enqueue.WORKER_KILL_GRACE_SECONDS,
        }
    ]
    assert enqueue.WORKER_KILL_GRACE_SECONDS > 0
