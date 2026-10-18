"""The notebook ⋮ menu and home: chat settings, copy, featured pins, chat history."""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from fastapi import HTTPException, Response
from pydantic import ValidationError
from sqlalchemy import select

from app.auth.context import AuthContext
from app.db import ARTIFACT_API_SOURCE, ChatVisibility, NewChatThread
from app.notebooks.chat.context_block import render_notebook_context
from app.notebooks.chat.pins import load_notebook_pins
from app.notebooks.persistence import NotebookNote, NotebookSource
from app.notebooks.routes import notebooks, threads
from app.notebooks.schemas import NotebookCopy, NotebookCreate, NotebookUpdate

pytestmark = pytest.mark.integration


async def _status(awaitable) -> int:
    with pytest.raises(HTTPException) as exc:
        await awaitable
    return exc.value.status_code


async def _create(db_session, owner, title="Research"):
    return await notebooks.create_notebook(
        owner.workspace.id, NotebookCreate(title=title), db_session, owner.auth
    )


async def test_chat_settings_persist_and_reach_the_turn_context(
    db_session, make_owner, make_document
):
    owner = await make_owner()
    created = await _create(db_session, owner)
    assert (created.chat_goal, created.response_length) == ("default", "default")

    updated = await notebooks.update_notebook(
        owner.workspace.id,
        created.id,
        NotebookUpdate(
            chat_goal="custom",
            chat_instructions="  Answer like a pediatric attending.  ",
            response_length="shorter",
        ),
        db_session,
        owner.auth,
    )
    assert updated.chat_goal == "custom"
    assert updated.chat_instructions == "Answer like a pediatric attending."
    assert updated.response_length == "shorter"

    thread = NewChatThread(
        title="t",
        workspace_id=owner.workspace.id,
        created_by_id=owner.user.id,
        notebook_id=created.id,
    )
    db_session.add(thread)
    await db_session.flush()
    pins = await load_notebook_pins(
        db_session, chat_id=thread.id, workspace_id=owner.workspace.id
    )
    block = render_notebook_context(pins)
    assert "Answer like a pediatric attending." in block
    assert "short, direct answers" in block
    assert "no enabled sources yet" in block


async def test_unknown_chat_settings_are_rejected():
    with pytest.raises(ValidationError):
        NotebookUpdate(chat_goal="pirate")
    with pytest.raises(ValidationError):
        NotebookUpdate(response_length="epic")


async def test_copy_takes_sources_and_notes_but_not_chats(
    db_session, make_owner, make_document
):
    owner = await make_owner()
    original = await _create(db_session, owner, "Asthma QI")
    doc_a = await make_document(owner.workspace.id, "A")
    doc_b = await make_document(owner.workspace.id, "B")
    db_session.add_all(
        [
            NotebookSource(notebook_id=original.id, document_id=doc_a.id),
            NotebookSource(
                notebook_id=original.id, document_id=doc_b.id, enabled=False
            ),
            NotebookNote(notebook_id=original.id, title="Note", content="Body"),
            NewChatThread(
                title="chat",
                workspace_id=owner.workspace.id,
                created_by_id=owner.user.id,
                notebook_id=original.id,
            ),
        ]
    )
    await db_session.flush()
    await notebooks.update_notebook(
        owner.workspace.id,
        original.id,
        NotebookUpdate(chat_goal="learning_guide"),
        db_session,
        owner.auth,
    )

    copy = await notebooks.copy_notebook_route(
        owner.workspace.id,
        original.id,
        NotebookCopy(title="Asthma QI (copy)"),
        db_session,
        owner.auth,
    )
    assert copy.id != original.id
    assert copy.title == "Asthma QI (copy)"
    assert (copy.source_count, copy.enabled_source_count) == (2, 1)
    assert copy.chat_goal == "learning_guide"
    notes = (
        await db_session.scalars(
            select(NotebookNote).where(NotebookNote.notebook_id == copy.id)
        )
    ).all()
    assert [(n.title, n.content) for n in notes] == [("Note", "Body")]
    chats = (
        await db_session.scalars(
            select(NewChatThread).where(NewChatThread.notebook_id == copy.id)
        )
    ).all()
    assert chats == []


async def test_copy_crosses_no_workspace_boundary(db_session, make_owner):
    owner = await make_owner()
    stranger = await make_owner()
    notebook = await _create(db_session, owner)
    assert (
        await _status(
            notebooks.copy_notebook_route(
                stranger.workspace.id,
                notebook.id,
                NotebookCopy(),
                db_session,
                stranger.auth,
            )
        )
        == 404
    )
    assert (
        await _status(
            notebooks.copy_notebook_route(
                owner.workspace.id,
                notebook.id,
                NotebookCopy(),
                db_session,
                stranger.auth,
            )
        )
        == 403
    )


async def test_featured_is_per_user(db_session, make_owner, make_user, add_member):
    owner = await make_owner()
    notebook = await _create(db_session, owner)
    featured = await notebooks.feature_notebook(
        owner.workspace.id, notebook.id, db_session, owner.auth
    )
    assert featured.featured is True
    # Idempotent.
    await notebooks.feature_notebook(
        owner.workspace.id, notebook.id, db_session, owner.auth
    )

    member = await make_user()
    await add_member(owner.workspace, member, "Editor")
    theirs = await notebooks.list_notebooks(
        owner.workspace.id, Response(), db_session, AuthContext.session(member)
    )
    assert [n.featured for n in theirs] == [False]
    mine = await notebooks.list_notebooks(
        owner.workspace.id, Response(), db_session, owner.auth
    )
    assert [n.featured for n in mine] == [True]

    cleared = await notebooks.unfeature_notebook(
        owner.workspace.id, notebook.id, db_session, owner.auth
    )
    assert cleared.featured is False


async def test_delete_chat_history_removes_only_my_own_chats(
    db_session, make_owner, make_user, add_member
):
    owner = await make_owner()
    notebook = await _create(db_session, owner)
    other = await make_user()
    await add_member(owner.workspace, other, "Editor")

    def chat(user, **extra):
        return NewChatThread(
            title="c",
            workspace_id=owner.workspace.id,
            created_by_id=user.id,
            notebook_id=notebook.id,
            **extra,
        )

    mine = chat(owner.user)
    studio = chat(owner.user, source=ARTIFACT_API_SOURCE)
    shared = chat(other, visibility=ChatVisibility.SEARCH_SPACE)
    db_session.add_all([mine, studio, shared])
    await db_session.flush()
    ids = SimpleNamespace(mine=mine.id, studio=studio.id, shared=shared.id)

    await threads.delete_notebook_chat_history(
        owner.workspace.id, notebook.id, db_session, owner.auth
    )
    remaining = set(
        (
            await db_session.scalars(
                select(NewChatThread.id).where(NewChatThread.notebook_id == notebook.id)
            )
        ).all()
    )
    assert remaining == {ids.studio, ids.shared}


async def test_viewer_cannot_delete_chat_history(
    db_session, make_owner, make_user, add_member
):
    owner = await make_owner()
    notebook = await _create(db_session, owner)
    viewer = await make_user()
    await add_member(owner.workspace, viewer, "Viewer")
    assert (
        await _status(
            threads.delete_notebook_chat_history(
                owner.workspace.id,
                notebook.id,
                db_session,
                AuthContext.session(viewer),
            )
        )
        == 403
    )
