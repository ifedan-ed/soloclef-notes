"""An automation run end to end on real Postgres, with a scripted model.

The real executor, step runner, agent graph (main agent, tools, middleware)
and billing meter run; only the chat model is the e2e stand-in
(``tests/e2e/fakes/chat_llm.py``), which follows a script keyed by the task
text. Every session the run opens is a sibling of the test's transaction, so
it sees the seeded rows and all of it rolls back.
"""

from __future__ import annotations

import uuid
from contextlib import asynccontextmanager

import pytest
from langgraph.checkpoint.memory import InMemorySaver
from sqlalchemy import select

import app.db as app_db
from app.agents.chat.runtime.llm_config import AgentConfig
from app.automations.actions.builtin.agent_task import invoke
from app.automations.actions.builtin.agent_task.dependencies import AgentDependencies
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun
from app.automations.runtime.executor import execute_run
from app.billing import ledger, meter
from app.billing.persistence.models import BillingUsage
from app.billing.seed import ensure_catalog
from app.billing.settings_store import save_settings
from app.billing.units import credits_to_micros
from app.db import Document, DocumentType
from app.notebooks.persistence import Notebook, NotebookNote, NotebookSource
from app.services.connector_service import ConnectorService
from tests.e2e.fakes.chat_llm import (
    AUTOMATION_ASKS_QUERY,
    AUTOMATION_NEEDS_WEB_QUERY,
    AUTOMATION_NEEDS_WEB_REASON,
    AUTOMATION_NOTEBOOKS_ANSWER,
    AUTOMATION_NOTEBOOKS_QUERY,
    FakeChatLLM,
)

pytestmark = pytest.mark.integration


def _agent_config(tier: str = "free") -> AgentConfig:
    return AgentConfig(
        provider="openai",
        model_name="e2e-fake",
        api_key="unused",
        config_id=-7,
        billing_tier=tier,
    )


@pytest.fixture
def scripted_model(db_session, monkeypatch):
    """The run's model is the scripted fake; its sessions join the test's."""
    siblings = meter.sibling_sessions(db_session)

    @asynccontextmanager
    async def the_test_session():
        # The run's own session is the test's (a sibling would open a
        # savepoint around the meter's and roll its writes back on close).
        yield db_session

    monkeypatch.setattr(invoke, "async_session_maker", the_test_session)
    monkeypatch.setattr(app_db, "shielded_async_session", siblings)
    monkeypatch.setattr(meter, "_default_factory", lambda: siblings)
    state = {"tier": "free", "raise": None}

    async def fake_dependencies(*, session, workspace_id, **_kwargs):
        if state["raise"] is not None:
            raise state["raise"]
        return AgentDependencies(
            llm=FakeChatLLM(),
            agent_config=_agent_config(state["tier"]),
            connector_service=ConnectorService(session, workspace_id),
            checkpointer=InMemorySaver(),
        )

    monkeypatch.setattr(invoke, "build_dependencies", fake_dependencies)
    return state


async def _billing(db_session, mode: str):
    await ensure_catalog(db_session)
    await save_settings(db_session, values={"mode": mode})


async def _seed_notebooks(db_session, workspace, user) -> list[Notebook]:
    notebooks = [
        Notebook(workspace_id=workspace.id, title=title, created_by_id=user.id)
        for title in ("Trip planning", "Reading list", "Grant ideas")
    ]
    db_session.add_all(notebooks)
    # A Studio output lives in /documents as a file; it is not a notebook.
    db_session.add(
        Document(
            workspace_id=workspace.id,
            title="Quiz_ Untitled notebook",
            document_type=DocumentType.ARTIFACT,
            content="quiz",
            content_hash=uuid.uuid4().hex,
            unique_identifier_hash=uuid.uuid4().hex,
            created_by_id=user.id,
        )
    )
    await db_session.flush()
    return notebooks


async def _run(
    db_session,
    workspace,
    user,
    query: str,
    *,
    execution: dict | None = None,
    automation_name: str = "Test",
    **params,
) -> AutomationRun:
    definition = {
        "schema_version": "1.0",
        "name": automation_name,
        "plan": [
            {
                "step_id": "step_1",
                "action": "agent_task",
                "params": {"query": query, "auto_approve_all": True, **params},
            }
        ],
        "execution": {
            "timeout_seconds": 120,
            "max_retries": 0,
            **(execution or {}),
        },
        "models": {"chat_model_id": -7, "image_gen_model_id": 0, "vision_model_id": 0},
    }
    automation = Automation(
        workspace_id=workspace.id,
        created_by_user_id=user.id,
        name=automation_name,
        definition=definition,
    )
    db_session.add(automation)
    await db_session.flush()
    run = AutomationRun(
        automation_id=automation.id,
        status=RunStatus.PENDING,
        definition_snapshot=definition,
        inputs={},
    )
    db_session.add(run)
    await db_session.flush()
    await execute_run(db_session, run.id)
    await db_session.refresh(run)
    return run


async def _usage(db_session, user) -> list[BillingUsage]:
    return list(
        (
            await db_session.scalars(
                select(BillingUsage).where(BillingUsage.user_id == user.id)
            )
        ).all()
    )


async def test_list_my_notebooks_lists_notebooks_not_studio_files(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    await _seed_notebooks(db_session, db_workspace, db_user)

    run = await _run(
        db_session, db_workspace, db_user, f"{AUTOMATION_NOTEBOOKS_QUERY} list them"
    )

    assert run.status == RunStatus.SUCCEEDED
    step = run.step_results[0]
    answer = step["result"]["final_message"]
    assert answer.startswith(AUTOMATION_NOTEBOOKS_ANSWER)
    for title in ("Trip planning", "Reading list", "Grant ideas"):
        assert title in answer
    assert "Quiz_" not in answer
    assert step["result"]["outcome"] == {
        "status": "succeeded",
        "reason": "Listed the notebooks.",
    }


async def test_a_step_charges_like_a_chat_message_on_its_tier(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "live")
    account, _ = await ledger.lock_account(db_session, db_user.id)
    before = account.plan_credits_micros + db_user.credit_micros_balance

    run = await _run(db_session, db_workspace, db_user, AUTOMATION_NOTEBOOKS_QUERY)

    assert run.status == RunStatus.SUCCEEDED
    [usage] = await _usage(db_session, db_user)
    assert (usage.action, usage.status, usage.mode) == (
        "chat_standard",
        "settled",
        "live",
    )
    assert usage.charged_micros == credits_to_micros(1)
    assert usage.details["source"] == "automation"
    assert usage.details["automation_run_id"] == run.id
    account, user = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_credits_micros + user.credit_micros_balance == before - (
        credits_to_micros(1)
    )


async def test_shadow_mode_records_without_charging(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    await _run(db_session, db_workspace, db_user, AUTOMATION_NOTEBOOKS_QUERY)
    [usage] = await _usage(db_session, db_user)
    assert (usage.mode, usage.status) == ("shadow", "settled")
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.reserved_micros == 0


async def test_an_exempt_account_is_recorded_as_exempt(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "live")
    account, _ = await ledger.lock_account(db_session, db_user.id)
    account.exempt = True
    await db_session.flush()
    await _run(db_session, db_workspace, db_user, AUTOMATION_NOTEBOOKS_QUERY)
    [usage] = await _usage(db_session, db_user)
    assert usage.mode == "exempt"


async def test_a_premium_model_on_a_free_plan_fails_the_step_before_it_runs(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "live")
    scripted_model["tier"] = "premium"

    run = await _run(db_session, db_workspace, db_user, AUTOMATION_NOTEBOOKS_QUERY)

    assert run.status == RunStatus.FAILED
    assert "plan doesn't include this step's model" in run.error["message"]
    [usage] = await _usage(db_session, db_user)
    assert usage.status == "denied"


async def test_a_step_that_raises_gives_the_hold_back(
    db_session, db_user, db_workspace, scripted_model, monkeypatch
):
    await _billing(db_session, "live")

    async def boom(**_kwargs):
        raise RuntimeError("model provider down")

    monkeypatch.setattr(invoke, "_invoke", boom)
    run = await _run(db_session, db_workspace, db_user, AUTOMATION_NOTEBOOKS_QUERY)

    assert run.status == RunStatus.FAILED
    [usage] = await _usage(db_session, db_user)
    assert usage.status == "released"
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.reserved_micros == 0


async def test_an_agent_that_reports_needs_attention_is_not_a_success(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    run = await _run(db_session, db_workspace, db_user, AUTOMATION_NEEDS_WEB_QUERY)

    assert run.status == RunStatus.NEEDS_ATTENTION
    assert run.error == {
        "message": AUTOMATION_NEEDS_WEB_REASON,
        "type": "NeedsAttention",
    }
    assert run.step_results[0]["status"] == "needs_attention"


async def test_an_answer_that_asks_a_question_needs_attention(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    run = await _run(db_session, db_workspace, db_user, AUTOMATION_ASKS_QUERY)
    assert run.status == RunStatus.NEEDS_ATTENTION
    assert "stopped to ask a question" in run.error["message"]


async def test_a_web_step_is_built_with_the_web_scope(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    run = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        retrieval_scope="web",
    )
    assert run.step_results[0]["result"]["retrieval_scope"] == "web"

    mentioned = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        mentioned_connector_ids=[4],
    )
    assert mentioned.step_results[0]["result"]["retrieval_scope"] == "all"


async def test_a_step_scoped_to_a_deleted_notebook_fails_with_a_reason(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    run = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        notebook_id=999999,
    )
    assert run.status == RunStatus.FAILED
    assert "no longer exists" in run.error["message"]


async def test_notebook_sources_are_listed_with_their_paths(
    db_session, db_user, db_workspace, scripted_model
):
    from app.automations.actions.builtin.agent_task.notebook_tools import (
        notebook_sources,
    )

    [notebook, *_] = await _seed_notebooks(db_session, db_workspace, db_user)
    source = Document(
        workspace_id=db_workspace.id,
        title="Field guide 2026",
        document_type=DocumentType.CRAWLED_URL,
        content="guideline",
        content_hash=uuid.uuid4().hex,
        unique_identifier_hash=uuid.uuid4().hex,
        document_metadata={"url": "https://example.org/guide"},
        created_by_id=db_user.id,
    )
    db_session.add(source)
    await db_session.flush()
    db_session.add(NotebookSource(notebook_id=notebook.id, document_id=source.id))
    await db_session.flush()

    found = await notebook_sources(db_session, db_workspace.id, notebook.id)
    [item] = found["sources"]
    assert item["title"] == "Field guide 2026"
    assert item["url"] == "https://example.org/guide"
    assert item["path"].startswith("/documents/")
    assert await notebook_sources(db_session, db_workspace.id + 1, notebook.id) is None


async def test_save_to_notebook_id_writes_a_note_titled_from_the_run(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    notebook = Notebook(
        workspace_id=db_workspace.id, title="Digests", created_by_id=db_user.id
    )
    db_session.add(notebook)
    await db_session.flush()

    run = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        save_to_notebook_id=notebook.id,
        automation_name="Daily digest",
    )

    assert run.status == RunStatus.SUCCEEDED
    [note] = (
        await db_session.scalars(
            select(NotebookNote).where(NotebookNote.notebook_id == notebook.id)
        )
    ).all()
    assert note.title.startswith("Daily digest — ")
    assert note.content.startswith(AUTOMATION_NOTEBOOKS_ANSWER)
    assert note.created_by_id == db_user.id
    # The Notes page says which automation wrote it.
    assert (note.origin, note.automation_id) == ("automation", run.automation_id)


async def test_save_to_notebook_id_is_a_no_op_when_unset(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    notebook = Notebook(
        workspace_id=db_workspace.id, title="Digests", created_by_id=db_user.id
    )
    db_session.add(notebook)
    await db_session.flush()

    await _run(db_session, db_workspace, db_user, AUTOMATION_NOTEBOOKS_QUERY)

    notes = (
        await db_session.scalars(
            select(NotebookNote).where(NotebookNote.notebook_id == notebook.id)
        )
    ).all()
    assert notes == []


async def test_save_to_a_deleted_notebook_fails_the_step(
    db_session, db_user, db_workspace, scripted_model
):
    await _billing(db_session, "shadow")
    # Checked before the agent is built, so no model is paid for.
    scripted_model["raise"] = RuntimeError("the agent was built")
    run = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        save_to_notebook_id=999999,
    )
    assert run.status == RunStatus.FAILED
    assert "no longer exists" in run.error["message"]


async def test_email_result_sends_the_owner_the_final_output(
    db_session, db_user, db_workspace, scripted_model, monkeypatch
):
    sent = []

    class _Outbox:
        async def send(self, message):
            sent.append(message)

    monkeypatch.setattr("app.automations.notify.delivery.is_mail_enabled", lambda: True)
    monkeypatch.setattr("app.automations.notify.delivery.get_mailer", lambda: _Outbox())
    await _billing(db_session, "shadow")

    run = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        execution={"email_result": True},
        automation_name="Daily digest",
    )

    assert run.status == RunStatus.SUCCEEDED
    assert len(sent) == 1
    assert sent[0].to == db_user.email
    assert "Daily digest" in sent[0].subject
    assert AUTOMATION_NOTEBOOKS_ANSWER in sent[0].text_body


async def test_email_result_off_by_default_sends_nothing(
    db_session, db_user, db_workspace, scripted_model, monkeypatch
):
    sent = []

    class _Outbox:
        async def send(self, message):
            sent.append(message)

    monkeypatch.setattr("app.automations.notify.delivery.is_mail_enabled", lambda: True)
    monkeypatch.setattr("app.automations.notify.delivery.get_mailer", lambda: _Outbox())
    await _billing(db_session, "shadow")

    await _run(db_session, db_workspace, db_user, AUTOMATION_NOTEBOOKS_QUERY)

    assert sent == []


async def test_email_result_on_failure_sends_a_short_failure_email(
    db_session, db_user, db_workspace, scripted_model, monkeypatch
):
    sent = []

    class _Outbox:
        async def send(self, message):
            sent.append(message)

    monkeypatch.setattr("app.automations.notify.delivery.is_mail_enabled", lambda: True)
    monkeypatch.setattr("app.automations.notify.delivery.get_mailer", lambda: _Outbox())
    await _billing(db_session, "shadow")

    run = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        execution={"email_result": True},
        notebook_id=999999,
    )

    assert run.status == RunStatus.FAILED
    assert len(sent) == 1
    assert "failed" in sent[0].subject
    assert "no longer exists" in sent[0].text_body


async def test_the_note_file_the_runs_agent_writes_is_stamped_with_its_automation(
    db_session, db_user, db_workspace, scripted_model, monkeypatch
):
    """Told to "save it as a note" with no notebook, the agent writes a file in
    /documents. Its own end-of-turn persistence stamps the file from the run's
    config; a note written in a chat meanwhile is not claimed."""
    from datetime import UTC, datetime

    from app.agents.chat.multi_agent_chat.main_agent.middleware.kb_persistence import (
        middleware as kb_persistence,
    )
    from app.config import config as app_config

    await _billing(db_session, "shadow")
    dim = app_config.embedding_model_instance.dimension
    monkeypatch.setattr(
        kb_persistence, "shielded_async_session", meter.sibling_sessions(db_session)
    )
    monkeypatch.setattr(
        kb_persistence, "embed_texts", lambda texts: [[0.0] * dim for _ in texts]
    )
    real_commit = kb_persistence.commit_staged_filesystem_state
    path = "/documents/notes/Email_Summary_2026-10-09.md"

    async def commit_with_a_staged_note(state, **kwargs):
        # What the knowledge-base subagent leaves staged after its write_file.
        staged = {
            **dict(state),
            "files": {path: {"content": ["Two unread emails."]}},
            "dirty_paths": [path],
        }
        return await real_commit(staged, **kwargs)

    monkeypatch.setattr(
        kb_persistence, "commit_staged_filesystem_state", commit_with_a_staged_note
    )
    chat_note = Document(
        workspace_id=db_workspace.id,
        title="My plan.md",
        document_type=DocumentType.NOTE,
        content="Plan.",
        content_hash=uuid.uuid4().hex,
        unique_identifier_hash=uuid.uuid4().hex,
        document_metadata={"virtual_path": "/documents/My plan.md"},
        created_by_id=db_user.id,
        created_at=datetime.now(UTC),
    )
    db_session.add(chat_note)
    await db_session.flush()

    run = await _run(
        db_session,
        db_workspace,
        db_user,
        AUTOMATION_NOTEBOOKS_QUERY,
        automation_name="Daily unread emails summary",
    )

    assert run.status == RunStatus.SUCCEEDED
    written = (
        await db_session.scalars(
            select(Document).where(
                Document.workspace_id == db_workspace.id,
                Document.title == "Email_Summary_2026-10-09.md",
            )
        )
    ).one()
    assert written.document_metadata == {
        "virtual_path": path,
        "automation_id": run.automation_id,
        "automation_name": "Daily unread emails summary",
        "automation_run_id": run.id,
    }
    await db_session.refresh(chat_note)
    assert chat_note.document_metadata == {"virtual_path": "/documents/My plan.md"}
