"""Unit tests drive the worker with fake sessions and no notebook outputs."""

from __future__ import annotations

import pytest


@pytest.fixture(autouse=True)
def _no_studio_plan(monkeypatch):
    """A job with no notebook output runs on the chat model, as chat jobs do."""
    from app.tasks.celery_tasks import deliverable_job_tasks as tasks

    async def no_plan(*_args, **_kwargs):
        return None

    monkeypatch.setattr(tasks, "plan_for_job", no_plan)
