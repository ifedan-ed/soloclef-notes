"""Imports are handed to the Celery worker under a registered task name."""

from __future__ import annotations

import uuid

import pytest

from app.celery_app import celery_app
from app.notebooks.research import import_task, seams

pytestmark = pytest.mark.unit


def test_task_is_registered_with_the_worker():
    assert "app.notebooks.research.import_task" in celery_app.conf.include
    assert import_task.import_research_sources_task.name == "notebook_research_import"


def test_default_dispatcher_queues_the_task_with_json_safe_arguments(monkeypatch):
    sent = []
    monkeypatch.setattr(
        import_task.import_research_sources_task,
        "delay",
        lambda *args: sent.append(args),
    )
    user = uuid.uuid4()
    seams.get_import_dispatcher()([3, 4], 7, user)
    assert sent == [([3, 4], 7, str(user))]
