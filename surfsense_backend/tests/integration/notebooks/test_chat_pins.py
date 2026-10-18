"""A notebook chat's enabled sources become its retrieval scope, and only its own."""

from __future__ import annotations

import pytest

from app.db import NewChatThread
from app.notebooks.chat.context_block import render_notebook_context
from app.notebooks.chat.pins import load_notebook_pins
from app.notebooks.persistence import NotebookSource
from app.tasks.chat.streaming.flows.new_chat.runtime_context import (
    build_new_chat_runtime_context,
)

pytestmark = pytest.mark.integration


async def _thread(db_session, workspace_id: int, notebook_id: int | None):
    thread = NewChatThread(
        title="chat", workspace_id=workspace_id, notebook_id=notebook_id
    )
    db_session.add(thread)
    await db_session.flush()
    return thread


async def test_only_enabled_sources_are_pinned(
    db_session, make_owner, make_document, make_folder, make_notebook
):
    owner = await make_owner()
    ws = owner.workspace.id
    enabled = await make_document(ws, title="Enabled")
    disabled = await make_document(ws, title="Disabled")
    folder = await make_folder(ws, name="Papers")
    notebook = await make_notebook(
        ws,
        sources=[
            ("document", enabled.id),
            ("document", disabled.id, False),
            ("folder", folder.id),
        ],
    )
    thread = await _thread(db_session, ws, notebook.id)

    pins = await load_notebook_pins(db_session, chat_id=thread.id, workspace_id=ws)

    assert pins.document_ids == [enabled.id]
    assert pins.folder_ids == [folder.id]
    assert pins.scope_document_ids == [enabled.id]
    block = render_notebook_context(pins)
    assert "Enabled" in block and "Papers/" in block and "Disabled" not in block


async def test_other_workspace_documents_are_never_pinned(
    db_session, make_owner, make_document, make_folder, make_notebook
):
    owner = await make_owner()
    other = await make_owner()
    mine = await make_document(owner.workspace.id, title="Mine")
    theirs = await make_document(other.workspace.id, title="Theirs")
    their_folder = await make_folder(other.workspace.id, name="TheirFolder")
    notebook = await make_notebook(owner.workspace.id, sources=[("document", mine.id)])
    # A row pointing across workspaces can only exist by bypassing the API;
    # the pin loader must still refuse it.
    db_session.add_all(
        [
            NotebookSource(notebook_id=notebook.id, document_id=theirs.id),
            NotebookSource(notebook_id=notebook.id, folder_id=their_folder.id),
        ]
    )
    await db_session.flush()
    thread = await _thread(db_session, owner.workspace.id, notebook.id)

    pins = await load_notebook_pins(
        db_session, chat_id=thread.id, workspace_id=owner.workspace.id
    )
    assert pins.document_ids == [mine.id]
    assert pins.folder_ids == []
    assert pins.scope_document_ids == [mine.id]

    # Nor does a thread asked about under the wrong workspace pick any up.
    assert (
        await load_notebook_pins(
            db_session, chat_id=thread.id, workspace_id=other.workspace.id
        )
        is None
    )


async def test_plain_chats_have_no_pins(db_session, make_owner):
    owner = await make_owner()
    thread = await _thread(db_session, owner.workspace.id, None)
    assert (
        await load_notebook_pins(
            db_session, chat_id=thread.id, workspace_id=owner.workspace.id
        )
        is None
    )
    assert render_notebook_context(None) is None


def test_runtime_context_carries_the_document_scope_beside_mentions():
    context = build_new_chat_runtime_context(
        workspace_id=1,
        mentioned_document_ids=[7],
        accepted_folder_ids=[],
        mentioned_folder_ids=None,
        mentioned_connector_ids=None,
        mentioned_connectors=None,
        request_id=None,
        turn_id="t",
        document_scope=[3, 4],
    )
    assert context.mentioned_document_ids == [7]
    assert context.document_scope == [3, 4]


def test_a_plain_chat_context_is_unscoped():
    context = build_new_chat_runtime_context(
        workspace_id=1,
        mentioned_document_ids=None,
        accepted_folder_ids=[],
        mentioned_folder_ids=None,
        mentioned_connector_ids=None,
        mentioned_connectors=None,
        request_id=None,
        turn_id="t",
    )
    assert context.document_scope is None
