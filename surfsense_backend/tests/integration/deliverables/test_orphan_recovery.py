"""A running video whose worker died is found, requeued and bounded — on Postgres.

The unit tests check the statements compile the right way; these run them,
because what matters here is row locking and guarded transitions, which only
a real database exercises.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import update
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import DeliverableFailureCode, DeliverableJob, DeliverableJobStatus
from app.deliverables.jobs.service import (
    claim_deliverable_job,
    create_deliverable_job,
    list_orphaned_running_jobs,
    touch_deliverable_job_heartbeat,
)
from app.tasks.celery_tasks import deliverable_job_tasks as tasks

pytestmark = pytest.mark.integration


async def _running_job(session: AsyncSession, workspace_id: int) -> DeliverableJob:
    job, _created = await create_deliverable_job(
        session,
        kind="video",
        title="Orphan",
        workspace_id=workspace_id,
        tool_call_id=f"call-orphan-{datetime.now(UTC).timestamp()}",
        request={"version": 1, "brief": "b", "source_references": []},
    )
    claimed = await claim_deliverable_job(session, job.id, task_id=job.celery_task_id)
    assert claimed is not None and claimed.status is DeliverableJobStatus.RUNNING
    return claimed


async def _age_heartbeat(session: AsyncSession, job_id: int, minutes: int) -> None:
    await session.execute(
        update(DeliverableJob)
        .where(DeliverableJob.id == job_id)
        .values(heartbeat_at=datetime.now(UTC) - timedelta(minutes=minutes))
    )


def _cutoff() -> datetime:
    return datetime.now(UTC) - tasks._RUNNING_STALE_AFTER


async def test_keepalive_keeps_a_live_job_out_of_the_orphan_list(
    db_session: AsyncSession, db_workspace
) -> None:
    job = await _running_job(db_session, db_workspace.id)
    await _age_heartbeat(db_session, job.id, 30)
    assert [
        j.id
        for j in await list_orphaned_running_jobs(
            db_session, heartbeat_before=_cutoff()
        )
    ] == [job.id]

    assert await touch_deliverable_job_heartbeat(
        db_session, job.id, task_id=job.celery_task_id
    )

    assert (
        await list_orphaned_running_jobs(db_session, heartbeat_before=_cutoff()) == []
    )


async def test_keepalive_from_a_superseded_attempt_changes_nothing(
    db_session: AsyncSession, db_workspace
) -> None:
    job = await _running_job(db_session, db_workspace.id)
    await _age_heartbeat(db_session, job.id, 30)

    assert not await touch_deliverable_job_heartbeat(
        db_session, job.id, task_id="deliverable-job:0:attempt:9"
    )
    assert [
        j.id
        for j in await list_orphaned_running_jobs(
            db_session, heartbeat_before=_cutoff()
        )
    ] == [job.id]


async def test_recovery_requeues_then_the_same_attempt_can_be_claimed(
    db_session: AsyncSession, db_workspace
) -> None:
    job = await _running_job(db_session, db_workspace.id)
    await _age_heartbeat(db_session, job.id, 30)
    owners: list[str] = []

    dispatch = await tasks._recover_orphaned_running(db_session, owners)

    assert dispatch == [(job.id, job.celery_task_id)]
    assert owners == [f"deliverable-job-{job.id}-attempt-1"]
    await db_session.refresh(job)
    assert job.status is DeliverableJobStatus.QUEUED
    assert job.checkpoint == {"orphan_requeues": 1}
    assert job.heartbeat_at is None

    # The republished task carries the same id, so the normal claim takes it.
    again = await claim_deliverable_job(db_session, job.id, task_id=job.celery_task_id)
    assert again is not None
    assert again.status is DeliverableJobStatus.RUNNING
    assert again.checkpoint == {"orphan_requeues": 1}


async def test_recovery_fails_a_job_that_keeps_losing_its_worker(
    db_session: AsyncSession, db_workspace
) -> None:
    job = await _running_job(db_session, db_workspace.id)
    await db_session.execute(
        update(DeliverableJob)
        .where(DeliverableJob.id == job.id)
        .values(checkpoint={"orphan_requeues": tasks._MAX_ORPHAN_REQUEUES})
    )
    await _age_heartbeat(db_session, job.id, 30)
    owners: list[str] = []

    assert await tasks._recover_orphaned_running(db_session, owners) == []

    await db_session.refresh(job)
    assert job.status is DeliverableJobStatus.FAILED
    assert job.failure_code == DeliverableFailureCode.RENDER_FAILED.value
    assert owners == [f"deliverable-job-{job.id}-attempt-1"]


async def test_a_fresh_running_job_is_not_touched(
    db_session: AsyncSession, db_workspace
) -> None:
    job = await _running_job(db_session, db_workspace.id)
    owners: list[str] = []

    assert await tasks._recover_orphaned_running(db_session, owners) == []

    await db_session.refresh(job)
    assert job.status is DeliverableJobStatus.RUNNING
    assert owners == []
