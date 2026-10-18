"""Gemini tool-call ids (a ``__thought__`` signature, far past 255 chars) save.

A Gemini benchmark failed with "value too long for type character varying(255)"
when a deliverable job was stored for such a call. These run on Postgres
because the limits that matter (column width, btree entry size, conflict
inference on an expression index) only exist there.
"""

from __future__ import annotations

import secrets

import pytest
from sqlalchemy import select, text
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import AgentActionLog, DocumentRevision, NewChatThread
from app.deliverables.jobs.service import create_deliverable_job

pytestmark = pytest.mark.integration

# The columns that store a LangChain tool-call id.
TOOL_CALL_ID_COLUMNS = {
    ("deliverable_jobs", "tool_call_id"),
    ("artifacts", "created_by_tool_call_id"),
    ("artifacts", "updated_by_tool_call_id"),
    ("agent_action_log", "tool_call_id"),
    ("agent_action_log", "turn_id"),
    ("agent_action_log", "message_id"),
    ("document_revisions", "created_by_turn_id"),
    ("folder_revisions", "created_by_turn_id"),
}


def _gemini_call_id(length: int) -> str:
    # Random, so TOAST compression cannot shrink it under a btree limit.
    prefix = "call_7f3a__thought__"
    return prefix + secrets.token_hex(length)[: length - len(prefix)]


async def test_every_tool_call_id_column_is_unbounded(db_session: AsyncSession):
    rows = await db_session.execute(
        text(
            "SELECT table_name, column_name, data_type "
            "FROM information_schema.columns WHERE table_schema = 'public'"
        )
    )
    types = {(r.table_name, r.column_name): r.data_type for r in rows}
    assert {col: types.get(col) for col in TOOL_CALL_ID_COLUMNS} == dict.fromkeys(
        TOOL_CALL_ID_COLUMNS, "text"
    )


@pytest.mark.parametrize("length", [1_000, 6_000])
async def test_deliverable_job_with_a_long_id_is_created_once(
    db_session: AsyncSession, db_workspace, length: int
):
    call_id = _gemini_call_id(length)
    first, created = await create_deliverable_job(
        db_session,
        kind="video",
        title="Gemini",
        workspace_id=db_workspace.id,
        tool_call_id=call_id,
        request={"version": 1, "brief": "b", "source_references": []},
    )
    again, created_again = await create_deliverable_job(
        db_session,
        kind="video",
        title="Gemini",
        workspace_id=db_workspace.id,
        tool_call_id=call_id,
        request={"version": 1, "brief": "b", "source_references": []},
    )

    assert created and not created_again
    assert again.id == first.id
    assert first.tool_call_id == call_id


async def test_ids_differing_only_at_the_end_stay_distinct_jobs(
    db_session: AsyncSession, db_workspace
):
    base = _gemini_call_id(1_000)
    ids = set()
    for suffix in ("a", "b"):
        job, created = await create_deliverable_job(
            db_session,
            kind="video",
            title="Gemini",
            workspace_id=db_workspace.id,
            tool_call_id=base + suffix,
            request={"version": 1, "brief": "b", "source_references": []},
        )
        assert created
        ids.add(job.id)
    assert len(ids) == 2


async def test_action_log_and_revision_keep_and_find_a_long_id(
    db_session: AsyncSession, db_workspace
):
    call_id = _gemini_call_id(6_000)
    thread = NewChatThread(
        title="Gemini", workspace_id=db_workspace.id, created_by_id=db_workspace.user_id
    )
    db_session.add(thread)
    await db_session.flush()
    action = AgentActionLog(
        thread_id=thread.id,
        workspace_id=db_workspace.id,
        tool_call_id=call_id,
        turn_id=call_id,
        tool_name="edit_file",
    )
    db_session.add(action)
    await db_session.flush()
    db_session.add(
        DocumentRevision(
            workspace_id=db_workspace.id,
            created_by_turn_id=call_id,
            agent_action_id=action.id,
        )
    )
    await db_session.flush()

    found = await db_session.scalar(
        select(AgentActionLog.id).where(AgentActionLog.tool_call_id.in_([call_id]))
    )
    revision = await db_session.scalar(
        select(DocumentRevision.agent_action_id).where(
            DocumentRevision.created_by_turn_id == call_id
        )
    )
    assert found == action.id
    assert revision == action.id


async def test_action_log_message_id_accepts_a_600_char_tool_call_id(
    db_session: AsyncSession, db_workspace
):
    """``c0007`` widened ``tool_call_id``/``turn_id`` but missed ``message_id``.

    ``ActionLogMiddleware._resolve_message_id`` stores the same LangChain
    ``tool_call.id`` in ``message_id``. Until ``c0018``, that column was
    ``VARCHAR(128)``, so a Gemini call id (which includes a thought
    signature, routinely 500+ chars) failed the whole INSERT with
    ``StringDataRightTruncationError`` even though ``tool_call_id``/``turn_id``
    on the same row were fine. Seen 2026-10-01 in production (thread 117).
    """
    call_id = _gemini_call_id(600)
    assert len(call_id) == 600
    thread = NewChatThread(
        title="Gemini", workspace_id=db_workspace.id, created_by_id=db_workspace.user_id
    )
    db_session.add(thread)
    await db_session.flush()
    action = AgentActionLog(
        thread_id=thread.id,
        workspace_id=db_workspace.id,
        tool_call_id=call_id,
        turn_id=call_id,
        message_id=call_id,
        tool_name="task",
    )
    db_session.add(action)
    await db_session.flush()

    found = await db_session.scalar(
        select(AgentActionLog.message_id).where(AgentActionLog.id == action.id)
    )
    assert found == call_id
