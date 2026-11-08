"""Database rules for notebooks: one target per source, no duplicates, safe deletes."""

from __future__ import annotations

import pytest
from sqlalchemy import select
from sqlalchemy.exc import IntegrityError

from app.db import Document, NewChatThread
from app.notebooks.persistence import Notebook, NotebookNote, NotebookSource

pytestmark = pytest.mark.integration


async def _expect_integrity_error(db_session, row):
    savepoint = await db_session.begin_nested()
    db_session.add(row)
    with pytest.raises(IntegrityError):
        await db_session.flush()
    await savepoint.rollback()


async def test_source_needs_exactly_one_target(
    db_session, make_owner, make_document, make_folder, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    document = await make_document(owner.workspace.id)
    folder = await make_folder(owner.workspace.id)

    await _expect_integrity_error(db_session, NotebookSource(notebook_id=notebook.id))
    await _expect_integrity_error(
        db_session,
        NotebookSource(
            notebook_id=notebook.id, document_id=document.id, folder_id=folder.id
        ),
    )


async def test_a_target_is_linked_once_per_notebook(
    db_session, make_owner, make_document, make_notebook
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id)
    notebook = await make_notebook(
        owner.workspace.id, sources=[("document", document.id)]
    )
    await _expect_integrity_error(
        db_session, NotebookSource(notebook_id=notebook.id, document_id=document.id)
    )


async def test_deleting_a_document_removes_its_source_rows(
    db_session, make_owner, make_document, make_notebook
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id)
    notebook = await make_notebook(
        owner.workspace.id, sources=[("document", document.id)]
    )
    notebook_id = notebook.id
    await db_session.delete(document)
    await db_session.flush()
    db_session.expire_all()

    remaining = (
        await db_session.scalars(
            select(NotebookSource).where(NotebookSource.notebook_id == notebook_id)
        )
    ).all()
    assert remaining == []


async def test_deleting_a_notebook_keeps_documents_and_detaches_chats(
    db_session, make_owner, make_document, make_notebook
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id)
    notebook = await make_notebook(
        owner.workspace.id, sources=[("document", document.id)]
    )
    note = NotebookNote(notebook_id=notebook.id, title="n", content="c")
    thread = NewChatThread(
        title="chat", workspace_id=owner.workspace.id, notebook_id=notebook.id
    )
    db_session.add_all([note, thread])
    await db_session.flush()
    note_id, thread_id, document_id = note.id, thread.id, document.id

    await db_session.delete(await db_session.get(Notebook, notebook.id))
    await db_session.flush()
    db_session.expire_all()

    assert await db_session.get(Document, document_id) is not None
    assert await db_session.get(NotebookNote, note_id) is None
    kept = await db_session.get(NewChatThread, thread_id)
    assert kept is not None and kept.notebook_id is None
