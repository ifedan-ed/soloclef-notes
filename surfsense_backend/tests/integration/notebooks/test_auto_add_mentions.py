"""@-mentioning a workspace document/folder adds it to a notebook's sources."""

from __future__ import annotations

import pytest

from app.db import NewChatThread
from app.notebooks.chat.auto_add_mentions import apply_mentioned_sources
from app.notebooks.persistence import NotebookSource

pytestmark = pytest.mark.integration


async def _thread(db_session, workspace_id: int, notebook_id: int):
    thread = NewChatThread(
        title="chat", workspace_id=workspace_id, notebook_id=notebook_id
    )
    db_session.add(thread)
    await db_session.flush()
    return thread


async def test_mentioning_a_document_outside_the_notebook_adds_it(
    db_session, make_owner, make_document, make_notebook
):
    owner = await make_owner()
    ws = owner.workspace.id
    existing = await make_document(ws, title="Existing")
    outsider = await make_document(ws, title="Outsider")
    notebook = await make_notebook(ws, sources=[("document", existing.id)])
    await _thread(db_session, ws, notebook.id)

    added = await apply_mentioned_sources(
        db_session,
        notebook_id=notebook.id,
        workspace_id=ws,
        auth=owner.auth,
        mentioned_document_ids=[outsider.id],
        mentioned_folder_ids=None,
    )
    await db_session.flush()

    assert added is True
    rows = (
        await db_session.execute(
            NotebookSource.__table__.select().where(
                NotebookSource.notebook_id == notebook.id
            )
        )
    ).all()
    doc_ids = {row.document_id for row in rows if row.document_id is not None}
    assert doc_ids == {existing.id, outsider.id}


async def test_mentioning_a_folder_adds_it_and_its_documents_become_in_scope(
    db_session, make_owner, make_document, make_folder, make_notebook
):
    owner = await make_owner()
    ws = owner.workspace.id
    folder = await make_folder(ws, name="Papers")
    in_folder = await make_document(ws, title="Paper 1", folder_id=folder.id)
    notebook = await make_notebook(ws, sources=[])

    added = await apply_mentioned_sources(
        db_session,
        notebook_id=notebook.id,
        workspace_id=ws,
        auth=owner.auth,
        mentioned_document_ids=None,
        mentioned_folder_ids=[folder.id],
    )
    await db_session.flush()

    assert added is True
    rows = (
        await db_session.execute(
            NotebookSource.__table__.select().where(
                NotebookSource.notebook_id == notebook.id
            )
        )
    ).all()
    assert {row.folder_id for row in rows if row.folder_id is not None} == {folder.id}
    assert in_folder.id  # sanity: document exists in the folder


async def test_already_a_source_is_a_no_op(
    db_session, make_owner, make_document, make_notebook
):
    owner = await make_owner()
    ws = owner.workspace.id
    doc = await make_document(ws, title="Doc")
    notebook = await make_notebook(ws, sources=[("document", doc.id)])

    added = await apply_mentioned_sources(
        db_session,
        notebook_id=notebook.id,
        workspace_id=ws,
        auth=owner.auth,
        mentioned_document_ids=[doc.id],
        mentioned_folder_ids=None,
    )

    assert added is False


async def test_a_document_from_another_workspace_is_refused(
    db_session, make_owner, make_document, make_notebook
):
    owner = await make_owner()
    other = await make_owner()
    theirs = await make_document(other.workspace.id, title="Theirs")
    notebook = await make_notebook(owner.workspace.id, sources=[])

    added = await apply_mentioned_sources(
        db_session,
        notebook_id=notebook.id,
        workspace_id=owner.workspace.id,
        auth=owner.auth,
        mentioned_document_ids=[theirs.id],
        mentioned_folder_ids=None,
    )

    assert added is False
    rows = (
        await db_session.execute(
            NotebookSource.__table__.select().where(
                NotebookSource.notebook_id == notebook.id
            )
        )
    ).all()
    assert rows == []


async def test_no_auth_context_is_refused(db_session, make_owner, make_document, make_notebook):
    owner = await make_owner()
    ws = owner.workspace.id
    doc = await make_document(ws, title="Doc")
    notebook = await make_notebook(ws, sources=[])

    added = await apply_mentioned_sources(
        db_session,
        notebook_id=notebook.id,
        workspace_id=ws,
        auth=None,
        mentioned_document_ids=[doc.id],
        mentioned_folder_ids=None,
    )

    assert added is False
