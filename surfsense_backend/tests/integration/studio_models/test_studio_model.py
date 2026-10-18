"""The Studio outputs model: stored settings, resolution, the route, the worker.

Real Postgres, real rows: which model a Studio output resolves to depends on
connection scope, ownership and enabled flags, which is exactly what a mock
would get wrong.
"""

from __future__ import annotations

import importlib.util
import uuid
from pathlib import Path
from types import SimpleNamespace

import litellm
import pytest
from fastapi import HTTPException
from sqlalchemy import select, text

from app.artifacts.persistence import Artifact
from app.auth.context import AuthContext
from app.db import (
    Connection,
    ConnectionScope,
    DeliverableJob,
    Document,
    DocumentType,
    Model,
    NewChatThread,
    User,
    Workspace,
)
from app.deliverables.jobs.service import claim_deliverable_job, create_deliverable_job
from app.notebooks.persistence import Notebook, NotebookOutput
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.studio_models import routes
from app.studio_models.choices import StudioChoice, StudioModelSettings
from app.studio_models.recommended import DEEPSEEK_FLASH, LUNA
from app.studio_models.resolution import resolve_plan
from app.studio_models.slots import STUDIO_SLOTS
from app.tasks.celery_tasks import deliverable_job_tasks as tasks

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[3]
    / "alembic"
    / "versions"
    / "c0006_workspace_studio_model.py"
)


async def _owner(db_session) -> SimpleNamespace:
    user = User(
        id=uuid.uuid4(),
        email=f"sm-{uuid.uuid4().hex[:8]}@surfsense.test",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    workspace = Workspace(name="Studio", user_id=user.id, chat_model_id=0)
    db_session.add(workspace)
    await db_session.flush()
    await create_default_roles_and_membership(db_session, workspace.id, user.id)
    await db_session.flush()
    return SimpleNamespace(
        user=user, workspace=workspace, auth=AuthContext.session(user)
    )


async def _model(db_session, owner, model_id: str, *, enabled: bool = True) -> Model:
    connection = Connection(
        provider="openai_compatible",
        base_url="http://litellm:4000/v1",
        api_key="sk-test",
        scope=ConnectionScope.USER,
        workspace_id=None,
        user_id=owner.user.id,
    )
    db_session.add(connection)
    await db_session.flush()
    model = Model(
        connection_id=connection.id,
        model_id=model_id,
        supports_chat=True,
        enabled=enabled,
    )
    db_session.add(model)
    await db_session.flush()
    return model


async def _measured(db_session, owner):
    return (
        await _model(db_session, owner, LUNA),
        await _model(db_session, owner, DEEPSEEK_FLASH),
    )


async def _save(db_session, owner, settings: dict):
    return await routes.update_studio_model(
        owner.workspace.id,
        StudioModelSettings.model_validate(settings),
        db_session,
        owner.auth,
    )


async def _plan(db_session, owner, slot):
    plan = await resolve_plan(db_session, owner.workspace, slot)
    return (
        plan.primary.model_id if plan.primary else None,
        plan.fallback.model_id if plan.fallback else None,
        plan.source,
    )


async def test_without_a_setting_studio_runs_on_the_measured_setup(db_session):
    owner = await _owner(db_session)
    await _measured(db_session, owner)

    assert await _plan(db_session, owner, "quiz") == (LUNA, DEEPSEEK_FLASH, "override")
    assert await _plan(db_session, owner, "video") == (LUNA, DEEPSEEK_FLASH, "override")
    assert await _plan(db_session, owner, "report_interactive") == (
        DEEPSEEK_FLASH,
        LUNA,
        "override",
    )
    read = await routes.get_studio_model(owner.workspace.id, db_session, owner.auth)
    assert not read.customized and read.unavailable == []
    assert read.default.mode == "recommended"
    assert set(read.overrides) == set(STUDIO_SLOTS)
    assert await _plan(db_session, owner, "slides") == (
        DEEPSEEK_FLASH,
        LUNA,
        "override",
    )
    assert await _plan(db_session, owner, "audio") == (LUNA, DEEPSEEK_FLASH, "override")


async def test_a_workspace_without_the_measured_models_stays_on_auto(db_session):
    owner = await _owner(db_session)
    stranger = await _owner(db_session)
    # Someone else's Luna is not this workspace's to use.
    await _model(db_session, stranger, LUNA)

    assert await _plan(db_session, owner, "quiz") == (None, None, "auto")
    read = await routes.get_studio_model(owner.workspace.id, db_session, owner.auth)
    assert read.unavailable == ["default"] and read.overrides == {}
    assert read.recommended_primary.id is None


async def test_override_then_default_then_auto(db_session):
    owner = await _owner(db_session)
    luna, flash = await _measured(db_session, owner)
    gemini = await _model(db_session, owner, "google-gemini-3.8-flash")
    await _save(
        db_session,
        owner,
        {
            "default": {"mode": "model", "primary": gemini.id, "fallback": luna.id},
            "overrides": {
                "quiz": {"mode": "model", "primary": flash.id},
                "audio": {"mode": "auto"},
            },
        },
    )

    assert await _plan(db_session, owner, "quiz") == (DEEPSEEK_FLASH, None, "override")
    assert await _plan(db_session, owner, "audio") == (None, None, "override")
    assert await _plan(db_session, owner, "mindmap") == (
        "google-gemini-3.8-flash",
        LUNA,
        "default",
    )

    flash.enabled = False
    await db_session.flush()
    assert await _plan(db_session, owner, "quiz") == (
        "google-gemini-3.8-flash",
        LUNA,
        "default",
    )

    gemini.enabled = False
    await db_session.flush()
    assert await _plan(db_session, owner, "quiz") == (None, None, "auto")
    read = await routes.get_studio_model(owner.workspace.id, db_session, owner.auth)
    assert read.customized and read.unavailable == ["default", "quiz"]


async def test_auto_everywhere_in_one_save_and_reset_to_the_recommended_setup(
    db_session,
):
    owner = await _owner(db_session)
    await _measured(db_session, owner)

    read = await _save(
        db_session, owner, {"default": {"mode": "auto"}, "overrides": {}}
    )
    assert read.customized and read.overrides == {}
    for slot in ("quiz", "video", "report_interactive", "slides"):
        assert await _plan(db_session, owner, slot) == (None, None, "auto")

    read = await routes.reset_studio_model(owner.workspace.id, db_session, owner.auth)
    assert not read.customized and read.default == StudioChoice(mode="recommended")
    assert await _plan(db_session, owner, "quiz") == (LUNA, DEEPSEEK_FLASH, "override")


async def test_the_route_refuses_an_unusable_or_foreign_model(db_session):
    owner = await _owner(db_session)
    stranger = await _owner(db_session)
    foreign = await _model(db_session, stranger, "theirs")
    off = await _model(db_session, owner, "off", enabled=False)
    mine = await _model(db_session, owner, "mine")

    for model_id in (foreign.id, off.id, 999_999):
        for settings in (
            {"default": {"mode": "model", "primary": model_id}},
            {"default": {"mode": "model", "primary": mine.id, "fallback": model_id}},
            {
                "default": {"mode": "auto"},
                "overrides": {"quiz": {"mode": "model", "primary": model_id}},
            },
        ):
            with pytest.raises(HTTPException) as refused:
                await _save(db_session, owner, settings)
            assert refused.value.status_code == 400


async def test_only_members_can_read_change_or_reset_it(db_session):
    owner = await _owner(db_session)
    stranger = await _owner(db_session)
    calls = (
        lambda: routes.get_studio_model(owner.workspace.id, db_session, stranger.auth),
        lambda: routes.update_studio_model(
            owner.workspace.id,
            StudioModelSettings(default=StudioChoice(mode="auto")),
            db_session,
            stranger.auth,
        ),
        lambda: routes.reset_studio_model(
            owner.workspace.id, db_session, stranger.auth
        ),
    )
    for call in calls:
        with pytest.raises(HTTPException) as refused:
            await call()
        assert refused.value.status_code == 403


async def test_the_migration_goes_up_and_down(db_session):
    spec = importlib.util.spec_from_file_location("c0006", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    connection = await db_session.connection()
    statements: list[str] = []
    module.op = SimpleNamespace(execute=statements.append)

    async def run(step) -> None:
        statements.clear()
        step()
        for statement in statements:
            await connection.execute(text(statement))

    async def exists() -> tuple[bool, bool]:
        table = await connection.scalar(
            text("SELECT to_regclass('workspace_studio_models') IS NOT NULL")
        )
        column = await connection.scalar(
            text(
                "SELECT count(*) FROM information_schema.columns WHERE "
                "table_name = 'notebook_outputs' AND column_name = 'generated_by'"
            )
        )
        return table, bool(column)

    await run(module.downgrade)
    assert await exists() == (False, False)
    await run(module.downgrade)  # idempotent
    await run(module.upgrade)
    await run(module.upgrade)  # idempotent
    assert await exists() == (True, True)

    owner = await _owner(db_session)
    read = await _save(db_session, owner, {"default": {"mode": "auto"}})
    assert read.default.mode == "auto"


async def _studio_quiz_job(db_session, owner) -> DeliverableJob:
    workspace = owner.workspace
    notebook = Notebook(workspace_id=workspace.id, title="Studio test")
    thread = NewChatThread(
        title="Quiz", workspace_id=workspace.id, created_by_id=owner.user.id
    )
    db_session.add_all([notebook, thread])
    await db_session.flush()
    job, _ = await create_deliverable_job(
        db_session,
        kind="artifact",
        title="Quiz",
        workspace_id=workspace.id,
        thread_id=thread.id,
        tool_call_id=f"notebook-studio:{notebook.id}:{uuid.uuid4().hex}",
        request={
            "version": 1,
            "output": "quiz",
            "prompt": "Create a quiz.",
            "root_thread_id": thread.id,
        },
    )
    db_session.add(
        NotebookOutput(
            notebook_id=notebook.id,
            workspace_id=workspace.id,
            kind="quiz",
            title="Quiz",
            options={"count": "fewer"},
            source_document_ids=[],
            deliverable_job_id=job.id,
            thread_id=thread.id,
        )
    )
    await db_session.flush()
    claimed = await claim_deliverable_job(
        db_session, job.id, task_id=job.celery_task_id
    )
    # Committed (to a savepoint), as the worker's claim is before it runs:
    # the fallback's rollback must not take the job with it.
    await db_session.commit()
    return claimed


async def _artifact(db_session, workspace_id: int) -> int:
    document = Document(
        title="Quiz",
        document_type=DocumentType.ARTIFACT,
        path=f"/artifacts/{uuid.uuid4().hex}.md",
        content="# Quiz",
        source_markdown="# Quiz",
        content_hash=uuid.uuid4().hex,
        workspace_id=workspace_id,
        status={"state": "ready"},
    )
    db_session.add(document)
    await db_session.flush()
    artifact = Artifact(
        document_id=document.id,
        workspace_id=workspace_id,
        format="quiz",
        artifact_metadata={"questions": 5},
    )
    db_session.add(artifact)
    await db_session.flush()
    return artifact.id


async def test_a_studio_quiz_falls_back_once_and_records_the_model(
    db_session, monkeypatch
):
    owner = await _owner(db_session)
    await _measured(db_session, owner)
    job = await _studio_quiz_job(db_session, owner)
    served = []

    async def bundle(_session, _workspace_id, model):
        return f"llm:{model.model_id}", SimpleNamespace(model_name=model.model_id)

    async def produce(session, job_arg, llm, agent_config, **_kwargs):
        served.append(llm)
        if llm == f"llm:{LUNA}":
            raise litellm.RateLimitError("slow down", llm_provider="openai", model="x")
        return await _artifact(session, job_arg.workspace_id)

    async def chat_model(*_args, **_kwargs):
        raise AssertionError("a Studio quiz must not run on the chat model")

    monkeypatch.setattr(tasks, "studio_llm_bundle", bundle)
    monkeypatch.setattr(tasks, "_produce", produce)
    monkeypatch.setattr(tasks, "_resolve_worker_model", chat_model)

    result = await tasks._execute_claimed_deliverable(
        db_session, job, job_id=job.id, task_id=job.celery_task_id
    )

    assert served == [f"llm:{LUNA}", f"llm:{DEEPSEEK_FLASH}"]
    expected = {
        "model": DEEPSEEK_FLASH,
        "setting": "override",
        "fallback": True,
        "primary": LUNA,
        "primary_error": "RateLimitError",
    }
    output = await db_session.scalar(
        select(NotebookOutput).where(NotebookOutput.deliverable_job_id == job.id)
    )
    await db_session.refresh(output)
    assert output.generated_by == expected
    metadata = await db_session.scalar(
        select(Artifact.artifact_metadata).where(Artifact.id == result["artifact_id"])
    )
    assert metadata == {"questions": 5, "generated_by": expected}


async def test_a_cancelled_studio_run_does_not_fall_back(db_session, monkeypatch):
    owner = await _owner(db_session)
    await _measured(db_session, owner)
    job = await _studio_quiz_job(db_session, owner)
    served = []

    async def bundle(_session, _workspace_id, model):
        return model.model_id, SimpleNamespace(model_name=model.model_id)

    async def produce(_session, _job, llm, _config, **_kwargs):
        served.append(llm)
        raise tasks.DeliverableJobCancellationError

    monkeypatch.setattr(tasks, "studio_llm_bundle", bundle)
    monkeypatch.setattr(tasks, "_produce", produce)

    with pytest.raises(tasks.DeliverableJobCancellationError):
        await tasks._execute_claimed_deliverable(
            db_session, job, job_id=job.id, task_id=job.celery_task_id
        )
    assert served == [LUNA]
