"""Long generation runs on a queue of its own, so it never holds up uploads."""

from __future__ import annotations

import pytest

from app.celery_app import (
    CELERY_TASK_DEFAULT_QUEUE,
    CONNECTORS_QUEUE,
    HEAVY_QUEUE,
    HEAVY_TASKS,
    celery_app,
)

pytestmark = pytest.mark.unit


def _queue_of(name: str) -> str:
    route = celery_app.amqp.router.route({}, name, args=(), kwargs={})
    return route["queue"].name


@pytest.mark.parametrize("name", HEAVY_TASKS)
def test_long_generation_goes_to_the_heavy_queue(name):
    assert _queue_of(name) == HEAVY_QUEUE == f"{CELERY_TASK_DEFAULT_QUEUE}.heavy"


def test_connector_indexing_keeps_its_queue_and_uploads_stay_fast():
    assert _queue_of("index_google_drive_files") == CONNECTORS_QUEUE
    assert _queue_of("process_file_upload") == CELERY_TASK_DEFAULT_QUEUE


def test_the_heavy_tasks_are_real_task_names():
    import app.deliverables.jobs.dispatch as dispatch
    import app.podcasts.tasks.draft
    import app.podcasts.tasks.render
    import app.tasks.celery_tasks.video_presentation_tasks  # noqa: F401

    registered = set(celery_app.tasks) | {dispatch.DELIVERABLE_JOB_TASK}
    assert set(HEAVY_TASKS) <= registered
