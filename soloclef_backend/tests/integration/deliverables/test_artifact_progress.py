"""A queued artifact's job row walks the Studio phases while the run works — on Postgres.

The subagent is scripted, but everything around it is real: the claim, the
first heartbeat, the progress writes through their own sessions, and the
guarded transition to ready.
"""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables import (
    run as deliverables_run,
)
from app.artifacts.persistence import Artifact
from app.db import (
    DeliverableJob,
    DeliverableJobStatus,
    Document,
    DocumentType,
    NewChatThread,
)
from app.deliverables.jobs.service import claim_deliverable_job, create_deliverable_job
from app.tasks.celery_tasks import deliverable_job_tasks as tasks

pytestmark = pytest.mark.integration


async def _claimed_artifact_job(session: AsyncSession, workspace) -> DeliverableJob:
    thread = NewChatThread(
        title="Studio", workspace_id=workspace.id, created_by_id=workspace.user_id
    )
    session.add(thread)
    await session.flush()
    job, _ = await create_deliverable_job(
        session,
        kind="artifact",
        title="Quiz: Iron",
        workspace_id=workspace.id,
        thread_id=thread.id,
        tool_call_id=f"studio:{uuid.uuid4().hex}",
        request={
            "version": 1,
            "output": "quiz",
            "prompt": "Create a quiz.",
            "root_thread_id": thread.id,
        },
    )
    claimed = await claim_deliverable_job(session, job.id, task_id=job.celery_task_id)
    assert claimed is not None
    return claimed


async def _chat_model(*_args, **_kwargs):
    """No notebook output, so no Studio plan: the job runs on the chat model."""
    return "llm", SimpleNamespace(is_premium=False, model_name="chat-model")


async def _saved_artifact(session: AsyncSession, workspace_id: int) -> int:
    document = Document(
        title="Quiz: Iron",
        document_type=DocumentType.ARTIFACT,
        path=f"/artifacts/{uuid.uuid4().hex}.md",
        content="# Quiz",
        source_markdown="# Quiz",
        content_hash=uuid.uuid4().hex,
        workspace_id=workspace_id,
        status={"state": "ready"},
    )
    session.add(document)
    await session.flush()
    artifact = Artifact(
        document_id=document.id, workspace_id=workspace_id, format="quiz"
    )
    session.add(artifact)
    await session.flush()
    return artifact.id


async def test_the_job_row_advances_through_every_phase_then_ends_ready(
    db_session: AsyncSession, db_workspace, monkeypatch
) -> None:
    job = await _claimed_artifact_job(db_session, db_workspace)
    connection = await db_session.connection()

    # Heartbeats open their own sessions; in the test they share the
    # rolled-back outer transaction so nothing outlives the test.
    def session_maker():
        return AsyncSession(
            bind=connection,
            expire_on_commit=False,
            join_transaction_mode="create_savepoint",
        )

    monkeypatch.setattr(tasks, "get_celery_session_maker", lambda: session_maker)
    monkeypatch.setattr(tasks, "_resolve_worker_model", _chat_model)

    observed: list[tuple[str, int]] = []

    async def row_state() -> None:
        row = (
            await db_session.execute(
                select(DeliverableJob.phase, DeliverableJob.progress).where(
                    DeliverableJob.id == job.id
                )
            )
        ).one()
        observed.append((row.phase, row.progress))

    async def scripted_run(*, session, workspace_id, callbacks, **_kwargs):
        (observer,) = callbacks
        await row_state()
        await observer.on_chat_model_start({}, [], run_id=uuid.uuid4())
        for name in (
            "load_artifact_instructions",
            "execute",
            "verify_artifact",
            "save_artifact",
        ):
            await observer.on_chat_model_start({}, [], run_id=uuid.uuid4())
            await row_state()
            tool_run = uuid.uuid4()
            await observer.on_tool_start({"name": name}, "", run_id=tool_run)
            await row_state()
            await observer.on_tool_end("ok", run_id=tool_run)
        artifact_id = await _saved_artifact(session, workspace_id)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=artifact_id, format="quiz")],
            message="Saved.",
        )

    monkeypatch.setattr(deliverables_run, "run_deliverable_subagent", scripted_run)

    result = await tasks._execute_claimed_deliverable(
        db_session, job, job_id=job.id, task_id=job.celery_task_id
    )

    assert result["status"] == "ready"
    phases = [phase for phase, _ in observed]
    distinct = [p for i, p in enumerate(phases) if i == 0 or phases[i - 1] != p]
    assert distinct == ["preparing", "drafting", "rendering", "verifying", "saving"]
    values = [progress for _, progress in observed]
    assert values == sorted(values) and max(values) < 100
    await db_session.refresh(job)
    assert job.status is DeliverableJobStatus.READY
    assert (job.phase, job.progress) == ("ready", 100)


async def test_progress_stops_quietly_once_the_job_is_cancelled(
    db_session: AsyncSession, db_workspace, monkeypatch
) -> None:
    job = await _claimed_artifact_job(db_session, db_workspace)
    monkeypatch.setattr(tasks, "_resolve_worker_model", _chat_model)
    connection = await db_session.connection()
    monkeypatch.setattr(
        tasks,
        "get_celery_session_maker",
        lambda: (
            lambda: AsyncSession(
                bind=connection,
                expire_on_commit=False,
                join_transaction_mode="create_savepoint",
            )
        ),
    )

    async def cancelled_mid_run(*, session, callbacks, **_kwargs):
        job_row = await session.get(DeliverableJob, job.id)
        job_row.status = DeliverableJobStatus.CANCELLING
        await session.flush()
        tool_run = uuid.uuid4()
        # The watcher, not the progress writer, ends a cancelled run.
        await callbacks[0].on_tool_start({"name": "execute"}, "", run_id=tool_run)
        raise tasks.DeliverableJobCancellationError

    monkeypatch.setattr(deliverables_run, "run_deliverable_subagent", cancelled_mid_run)

    with pytest.raises(tasks.DeliverableJobCancellationError):
        await tasks._execute_claimed_deliverable(
            db_session, job, job_id=job.id, task_id=job.celery_task_id
        )
    await db_session.refresh(job)
    assert (job.status, job.phase) == (DeliverableJobStatus.CANCELLING, "preparing")
