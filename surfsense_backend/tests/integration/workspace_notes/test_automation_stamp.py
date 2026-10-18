"""A note file says which automation run wrote it, from the moment it is written.

An automation run's agent and a chat (or a second automation) write note files
in the same minute, by the same person. Each agent's end-of-turn persistence
stamps only what its own turn created, from its own config, so nothing is
matched by time. Real filesystem tools and persistence middleware in a real
graph; only the model is scripted.
"""

from __future__ import annotations

import pytest
from langchain.agents import create_agent
from langchain_core.messages import HumanMessage
from langgraph.checkpoint.memory import InMemorySaver
from sqlalchemy import select

from app.agents.chat.multi_agent_chat.main_agent.middleware.kb_persistence import (
    middleware as kb_persistence,
)
from app.agents.chat.multi_agent_chat.shared.filesystem_selection import (
    FilesystemMode,
    FilesystemSelection,
)
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem import (
    build_filesystem_mw,
)
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.backends.resolver import (
    build_backend_resolver,
)
from app.automations.persistence.models.automation import Automation
from app.billing.meter import sibling_sessions
from app.config import config as app_config
from app.db import Document
from app.workspace_notes.automation_stamp import CONFIG_KEY, note_stamp
from app.workspace_notes.listing import list_workspace_notes
from tests.integration.harness import ScriptedTurn, build_scripted_harness

pytestmark = pytest.mark.integration


@pytest.fixture(autouse=True)
def _commits_join_the_test(db_session, monkeypatch):
    dim = app_config.embedding_model_instance.dimension
    monkeypatch.setattr(
        kb_persistence, "shielded_async_session", sibling_sessions(db_session)
    )
    monkeypatch.setattr(
        kb_persistence, "embed_texts", lambda texts: [[0.0] * dim for _ in texts]
    )


async def _write_a_note(owner, path: str, text: str, *, thread: str, stamp=None):
    """One agent turn that writes ``path``, committed at its end as in production."""
    workspace_id, user_id = owner.workspace.id, str(owner.user.id)
    resolver = build_backend_resolver(
        FilesystemSelection(mode=FilesystemMode.CLOUD), workspace_id=workspace_id
    )
    harness = build_scripted_harness(
        turns=[
            ScriptedTurn(
                tool_calls=[
                    {
                        "name": "write_file",
                        "args": {"file_path": path, "content": text},
                        "id": f"call-{thread}",
                    }
                ]
            ),
            ScriptedTurn(text="Saved."),
        ]
    )
    agent = create_agent(
        harness.model,
        tools=[],
        middleware=[
            build_filesystem_mw(
                backend_resolver=resolver,
                filesystem_mode=FilesystemMode.CLOUD,
                workspace_id=workspace_id,
                user_id=user_id,
                thread_id=None,
                read_only=False,
            ),
            kb_persistence.KnowledgeBasePersistenceMiddleware(
                workspace_id=workspace_id,
                created_by_id=user_id,
                filesystem_mode=FilesystemMode.CLOUD,
            ),
        ],
        checkpointer=InMemorySaver(),
    )
    configurable = {"thread_id": thread}
    if stamp is not None:
        configurable[CONFIG_KEY] = stamp
    await agent.ainvoke(
        {"messages": [HumanMessage(content="write it")]},
        config={"configurable": configurable},
    )


async def _automation(db_session, owner, name: str) -> Automation:
    automation = Automation(
        workspace_id=owner.workspace.id,
        created_by_user_id=owner.user.id,
        name=name,
        definition={"schema_version": "1.0", "name": name, "plan": []},
    )
    db_session.add(automation)
    await db_session.flush()
    return automation


async def test_each_writer_stamps_only_its_own_note(db_session, make_owner):
    owner = await make_owner()
    mail = await _automation(db_session, owner, "Mail")
    news = await _automation(db_session, owner, "News")

    # Same person, same minute: two automation runs and a chat.
    await _write_a_note(
        owner,
        "/documents/notes/Mail_2026-10-08.md",
        "Two unread emails.",
        thread="run-mail",
        stamp=note_stamp(automation_id=mail.id, automation_name="Mail", run_id=41),
    )
    await _write_a_note(
        owner,
        "/documents/notes/My chat plan.md",
        "Plan for the week.",
        thread="chat-7",
    )
    await _write_a_note(
        owner,
        "/documents/notes/News_2026-10-08.md",
        "Nothing new.",
        thread="run-news",
        stamp=note_stamp(automation_id=news.id, automation_name="News", run_id=42),
    )

    documents = (
        await db_session.scalars(
            select(Document).where(Document.workspace_id == owner.workspace.id)
        )
    ).all()
    stamps = {
        d.title: (
            d.document_metadata.get("automation_id"),
            d.document_metadata.get("automation_run_id"),
        )
        for d in documents
    }
    assert stamps == {
        "Mail_2026-10-08.md": (mail.id, 41),
        "My chat plan.md": (None, None),
        "News_2026-10-08.md": (news.id, 42),
    }
    assert all(d.created_by_id == owner.user.id for d in documents)

    listed = {
        row.title: (row.origin, row.automation.name if row.automation else None)
        for row in await list_workspace_notes(
            db_session, owner.workspace.id, query=None, include_documents=True
        )
    }
    assert listed == {
        "Mail_2026-10-08": ("automation", "Mail"),
        "My chat plan": ("chat", None),
        "News_2026-10-08": ("automation", "News"),
    }
