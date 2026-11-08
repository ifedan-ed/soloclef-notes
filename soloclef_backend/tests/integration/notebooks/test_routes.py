"""Notebook routes through their handlers: CRUD, sources, notes, workspace boundaries."""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import HTTPException, Response
from sqlalchemy import select

from app.auth.context import AuthContext
from app.db import Document, DocumentType, NewChatThread
from app.notebooks import note_conversion
from app.notebooks.routes import notebooks, notes, sources, threads
from app.notebooks.schemas import (
    NotebookCreate,
    NotebookUpdate,
    NoteCreate,
    SourcesAdd,
    SourcesBulkToggle,
    SourceTarget,
    SourceToggle,
)

pytestmark = pytest.mark.integration


async def _status(awaitable) -> int:
    with pytest.raises(HTTPException) as exc:
        await awaitable
    return exc.value.status_code


async def _create(db_session, owner, title="Research"):
    return await notebooks.create_notebook(
        owner.workspace.id, NotebookCreate(title=title), db_session, owner.auth
    )


async def test_create_list_rename_delete(db_session, make_owner):
    owner = await make_owner()
    created = await _create(db_session, owner)
    listed = await notebooks.list_notebooks(
        owner.workspace.id, Response(), db_session, owner.auth
    )
    assert [n.id for n in listed] == [created.id]

    renamed = await notebooks.update_notebook(
        owner.workspace.id,
        created.id,
        NotebookUpdate(title="Renamed", icon="📚"),
        db_session,
        owner.auth,
    )
    assert (renamed.title, renamed.icon) == ("Renamed", "📚")

    await notebooks.delete_notebook(
        owner.workspace.id, created.id, db_session, owner.auth
    )
    assert (
        await notebooks.list_notebooks(
            owner.workspace.id, Response(), db_session, owner.auth
        )
        == []
    )


async def test_outsider_cannot_see_or_touch_a_notebook(db_session, make_owner):
    owner = await make_owner()
    outsider = await make_owner()
    notebook = await _create(db_session, owner)

    # Not a member of the owner's workspace: refused at the workspace.
    assert (
        await _status(
            notebooks.list_notebooks(
                owner.workspace.id, Response(), db_session, outsider.auth
            )
        )
        == 403
    )
    assert (
        await _status(
            notebooks.get_notebook(
                owner.workspace.id, notebook.id, Response(), db_session, outsider.auth
            )
        )
        == 403
    )
    # Through their own workspace the notebook simply does not exist.
    assert (
        await _status(
            notebooks.get_notebook(
                outsider.workspace.id,
                notebook.id,
                Response(),
                db_session,
                outsider.auth,
            )
        )
        == 404
    )
    assert (
        await _status(
            notebooks.delete_notebook(
                outsider.workspace.id, notebook.id, db_session, outsider.auth
            )
        )
        == 404
    )
    assert (
        await _status(
            notes.create_note(
                outsider.workspace.id,
                notebook.id,
                NoteCreate(title="x"),
                db_session,
                outsider.auth,
            )
        )
        == 404
    )


async def test_viewer_can_read_but_not_create(
    db_session, make_owner, make_user, add_member
):
    owner = await make_owner()
    notebook = await _create(db_session, owner)
    viewer = await make_user()
    await add_member(owner.workspace, viewer, "Viewer")
    viewer_auth = AuthContext.session(viewer)

    listed = await notebooks.list_notebooks(
        owner.workspace.id, Response(), db_session, viewer_auth
    )
    assert [n.id for n in listed] == [notebook.id]
    assert (
        await _status(
            _create(
                db_session, SimpleNamespace(workspace=owner.workspace, auth=viewer_auth)
            )
        )
        == 403
    )


async def test_sources_cannot_come_from_another_workspace(
    db_session, make_owner, make_document
):
    owner = await make_owner()
    other = await make_owner()
    notebook = await _create(db_session, owner)
    foreign = await make_document(other.workspace.id, title="Secret")

    status = await _status(
        sources.add_sources(
            owner.workspace.id,
            notebook.id,
            SourcesAdd(targets=[SourceTarget(kind="document", id=foreign.id)]),
            db_session,
            owner.auth,
        )
    )
    assert status == 404


async def test_add_toggle_select_all_and_remove(
    db_session, make_owner, make_document, make_folder
):
    owner = await make_owner()
    notebook = await _create(db_session, owner)
    first = await make_document(owner.workspace.id, title="First")
    second = await make_document(owner.workspace.id, title="Second")
    folder = await make_folder(owner.workspace.id, name="Papers")

    listed = await sources.add_sources(
        owner.workspace.id,
        notebook.id,
        SourcesAdd(
            targets=[
                SourceTarget(kind="document", id=first.id),
                SourceTarget(kind="document", id=second.id),
                SourceTarget(kind="folder", id=folder.id),
                SourceTarget(kind="document", id=first.id),
            ]
        ),
        db_session,
        owner.auth,
    )
    assert sorted(s.title for s in listed) == ["First", "Papers", "Second"]
    assert all(s.enabled for s in listed)

    # Adding the same targets again is a no-op, not an error.
    again = await sources.add_sources(
        owner.workspace.id,
        notebook.id,
        SourcesAdd(targets=[SourceTarget(kind="document", id=first.id)]),
        db_session,
        owner.auth,
    )
    assert len(again) == 3

    first_source = next(s for s in listed if s.title == "First")
    toggled = await sources.toggle_source(
        owner.workspace.id,
        notebook.id,
        first_source.id,
        SourceToggle(enabled=False),
        db_session,
        owner.auth,
    )
    assert toggled.enabled is False

    cleared = await sources.toggle_sources(
        owner.workspace.id,
        notebook.id,
        SourcesBulkToggle(enabled=False),
        db_session,
        owner.auth,
    )
    assert not any(s.enabled for s in cleared)
    selected = await sources.toggle_sources(
        owner.workspace.id,
        notebook.id,
        SourcesBulkToggle(enabled=True),
        db_session,
        owner.auth,
    )
    assert all(s.enabled for s in selected)

    counted = await notebooks.get_notebook(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )
    assert (counted.source_count, counted.enabled_source_count) == (3, 3)

    await sources.remove_source(
        owner.workspace.id, notebook.id, first_source.id, db_session, owner.auth
    )
    remaining = await sources.get_sources(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )
    assert sorted(s.title for s in remaining) == ["Papers", "Second"]
    # The document itself is untouched.
    assert await db_session.get(Document, first.id) is not None


async def test_note_converts_into_a_workspace_source(
    db_session, make_owner, monkeypatch
):
    owner = await make_owner()
    notebook = await _create(db_session, owner)
    queued: list[int] = []
    monkeypatch.setattr(
        note_conversion, "_queue_reindex", lambda doc_id, _user: queued.append(doc_id)
    )
    monkeypatch.setattr(
        note_conversion, "record_prepared_documents", AsyncMock(return_value=None)
    )
    note = await notes.create_note(
        owner.workspace.id,
        notebook.id,
        NoteCreate(title="Key findings", content="# Key findings\n\nIron matters."),
        db_session,
        owner.auth,
    )

    listed = await notes.convert_note(
        owner.workspace.id, notebook.id, note.id, db_session, owner.auth
    )

    document = await db_session.scalar(
        select(Document).where(Document.title == "Key findings")
    )
    assert document is not None
    assert document.workspace_id == owner.workspace.id
    assert document.document_type == DocumentType.NOTE
    assert document.source_markdown.startswith("# Key findings")
    assert [s.target_id for s in listed] == [document.id]
    assert queued == [document.id]

    assert (
        await _status(
            notes.convert_note(
                owner.workspace.id, notebook.id, note.id, db_session, owner.auth
            )
        )
        == 409
    )


async def test_notebook_threads_are_bound_and_private(
    db_session, make_owner, make_user, add_member
):
    owner = await make_owner()
    notebook = await _create(db_session, owner)
    created = await threads.create_notebook_thread(
        owner.workspace.id, notebook.id, db_session, owner.auth
    )
    thread = await db_session.get(NewChatThread, created.id)
    assert thread.notebook_id == notebook.id

    listed = await threads.list_notebook_threads(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )
    assert [t.id for t in listed] == [created.id]

    # Another member sees the notebook but not the owner's private chat.
    editor = await make_user()
    await add_member(owner.workspace, editor, "Editor")
    theirs = await threads.list_notebook_threads(
        owner.workspace.id,
        notebook.id,
        Response(),
        db_session,
        AuthContext.session(editor),
    )
    assert theirs == []
