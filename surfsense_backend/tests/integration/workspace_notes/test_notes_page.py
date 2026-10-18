"""The Notes page: every note in a workspace, and moving a note into a notebook."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from unittest.mock import AsyncMock

import pytest
from fastapi import HTTPException, Response
from sqlalchemy import select

from app.automations.persistence.models.automation import Automation
from app.db import Document, Folder
from app.notebooks.persistence import NotebookNote
from app.notebooks.routes import notes as notebook_notes
from app.notebooks.schemas import NoteCopy, NoteCreate
from app.workspace_notes import moving, routes as notes_page
from app.workspace_notes.schemas import MoveIntoNotebook

pytestmark = pytest.mark.integration

_DAY = timedelta(days=1)


@pytest.fixture(autouse=True)
def _no_git_store(monkeypatch):
    """The move records the deletion in the workspace's git store; these
    workspaces have none, and what is recorded is not under test here."""
    monkeypatch.setattr(
        moving.knowledge_store, "record_deleted_documents", AsyncMock(return_value=None)
    )


async def _status(awaitable) -> int:
    with pytest.raises(HTTPException) as exc:
        await awaitable
    return exc.value.status_code


async def _list(owner, q: str | None = None, db_session=None):
    return await notes_page.list_notes(
        owner.workspace.id, Response(), q, db_session, owner.auth
    )


async def _automation(db_session, owner, name="Daily unread emails summary"):
    automation = Automation(
        workspace_id=owner.workspace.id,
        created_by_user_id=owner.user.id,
        name=name,
        definition={"schema_version": "1.0", "name": name, "plan": []},
    )
    db_session.add(automation)
    await db_session.flush()
    return automation


async def test_every_note_is_listed_newest_first_with_where_it_lives_and_who_made_it(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    automation = await _automation(db_session, owner)
    inbox = await make_notebook(owner.workspace.id, "Inbox")
    now = datetime.now(UTC)

    by_hand = NotebookNote(
        notebook_id=inbox.id,
        title="Ideas",
        content="**Buy** milk",
        created_at=now - 3 * _DAY,
        updated_at=now - 3 * _DAY,
    )
    saved = NotebookNote(
        notebook_id=inbox.id,
        title="Daily unread emails summary — 2026-10-08",
        content="Two emails.",
        origin="automation",
        automation_id=automation.id,
        created_at=now - _DAY,
        updated_at=now - _DAY,
    )
    db_session.add_all([by_hand, saved])
    file_note = await make_note_document(
        owner.workspace.id,
        "Email_Summary_2026-10-09.md",
        "# Unread\n\n- Ann: lunch?",
        created_by_id=owner.user.id,
        created_at=now,
        metadata={
            "virtual_path": "/documents/notes/Email_Summary_2026-10-09.md",
            "automation_id": automation.id,
            "automation_name": automation.name,
        },
    )
    chat_note = await make_note_document(
        owner.workspace.id, "Plan.md", created_at=now - 2 * _DAY
    )
    await db_session.flush()

    listed = await _list(owner, db_session=db_session)

    assert [(n.kind, n.id) for n in listed] == [
        ("document", file_note.id),
        ("notebook_note", saved.id),
        ("document", chat_note.id),
        ("notebook_note", by_hand.id),
    ]
    first = listed[0]
    assert first.title == "Email_Summary_2026-10-09"
    assert first.preview == "Unread - Ann: lunch?"
    assert first.origin == "automation"
    assert first.notebook is None
    assert first.automation.name == "Daily unread emails summary"
    assert listed[1].notebook.title == "Inbox"
    assert (listed[1].origin, listed[1].automation.id) == ("automation", automation.id)
    assert (listed[2].origin, listed[2].automation) == ("chat", None)
    assert (listed[3].origin, listed[3].preview) == ("hand", "Buy milk")


async def test_search_matches_titles_and_text_in_any_case(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    db_session.add(
        NotebookNote(notebook_id=notebook.id, title="Groceries", content="eggs")
    )
    await make_note_document(owner.workspace.id, "Inbox.md", "Ann asks about LUNCH")
    await make_note_document(owner.workspace.id, "100%_done.md", "finished")
    await db_session.flush()

    assert [n.title for n in await _list(owner, "lunch", db_session)] == ["Inbox"]
    assert [n.title for n in await _list(owner, "GROCER", db_session)] == ["Groceries"]
    # Wildcards are literal.
    assert [n.title for n in await _list(owner, "0%_", db_session)] == ["100%_done"]
    assert await _list(owner, "nothing like it", db_session) == []


async def test_a_converted_note_is_listed_once_and_a_source_says_where(
    db_session, make_owner, make_notebook, make_folder, make_note_document
):
    owner = await make_owner()
    folder = await make_folder(owner.workspace.id, "notes")
    in_folder = await make_note_document(
        owner.workspace.id, "a.md", folder_id=folder.id
    )
    twin = await make_note_document(owner.workspace.id, "Key findings")
    reading = await make_notebook(
        owner.workspace.id, "Reading", sources=[("folder", folder.id)]
    )
    db_session.add(
        NotebookNote(
            notebook_id=reading.id,
            title="Key findings",
            content="x",
            converted_document_id=twin.id,
        )
    )
    await db_session.flush()

    listed = await _list(owner, db_session=db_session)

    assert sorted((n.kind, n.title) for n in listed) == [
        ("document", "a"),
        ("notebook_note", "Key findings"),
    ]
    by_title = {n.title: n for n in listed}
    assert [r.title for r in by_title["a"].source_in] == ["Reading"]
    assert [r.title for r in by_title["Key findings"].source_in] == ["Reading"]
    assert in_folder.id == by_title["a"].id


async def test_a_note_document_says_which_folder_it_is_in(
    db_session, make_owner, make_notebook, make_folder, make_note_document
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id, "Inbox")
    projects = await make_folder(owner.workspace.id, "Projects")
    notes = Folder(
        name="notes",
        position="1",
        parent_id=projects.id,
        workspace_id=owner.workspace.id,
    )
    db_session.add(notes)
    await db_session.flush()
    db_session.add(NotebookNote(notebook_id=notebook.id, title="Ideas", content=""))
    nested = await make_note_document(
        owner.workspace.id, "Summary.md", folder_id=notes.id
    )
    top = await make_note_document(owner.workspace.id, "Loose.md")
    await db_session.flush()

    listed = {(n.kind, n.title): n for n in await _list(owner, db_session=db_session)}

    in_folder = listed[("document", "Summary")].folder
    assert (in_folder.id, in_folder.name, in_folder.path) == (
        notes.id,
        "notes",
        ["Projects", "notes"],
    )
    # Below Projects in the tree, so after it.
    assert in_folder.tree_order == 1
    assert listed[("document", "Loose")].folder is None
    assert listed[("notebook_note", "Ideas")].folder is None
    assert {nested.id, top.id} == {
        n.id for n in listed.values() if n.kind == "document"
    }


async def test_outsiders_see_nothing(db_session, make_owner):
    owner = await make_owner()
    outsider = await make_owner()
    assert (
        await _status(
            notes_page.list_notes(
                owner.workspace.id, Response(), None, db_session, outsider.auth
            )
        )
        == 403
    )


async def test_moving_a_note_document_makes_it_that_notebooks_note(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    automation = await _automation(db_session, owner)
    notebook = await make_notebook(owner.workspace.id, "Email")
    written = datetime(2026, 10, 9, 12, 2, tzinfo=UTC)
    document = await make_note_document(
        owner.workspace.id,
        "Email_Summary_2026-10-09.md",
        "# Unread\n\nNothing new.",
        created_by_id=owner.user.id,
        created_at=written,
        metadata={
            "virtual_path": "/documents/notes/Email_Summary_2026-10-09.md",
            "automation_id": automation.id,
            "automation_name": automation.name,
        },
    )
    document_id = document.id

    note = await notes_page.move_note_document(
        owner.workspace.id,
        document_id,
        MoveIntoNotebook(notebook_id=notebook.id),
        db_session,
        owner.auth,
    )

    assert note.notebook_id == notebook.id
    assert note.title == "Email_Summary_2026-10-09"
    assert note.content == "# Unread\n\nNothing new."
    assert (note.origin, note.automation_id) == ("automation", automation.id)
    assert note.created_at == written
    assert note.converted_document_id is None
    assert await db_session.get(Document, document_id) is None
    # It now shows in the notebook's Studio list, and once on the Notes page.
    studio = await notebook_notes.list_notes(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )
    assert [n.id for n in studio] == [note.id]
    assert [(n.kind, n.id) for n in await _list(owner, db_session=db_session)] == [
        ("notebook_note", note.id)
    ]


async def test_a_note_document_in_use_as_a_source_stays_where_it_is(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    document = await make_note_document(owner.workspace.id, "Findings.md")
    here = await make_notebook(
        owner.workspace.id, "Here", sources=[("document", document.id)]
    )
    elsewhere = await make_notebook(owner.workspace.id, "Elsewhere")

    into_here = await notes_page.move_note_document(
        owner.workspace.id,
        document.id,
        MoveIntoNotebook(notebook_id=here.id),
        db_session,
        owner.auth,
    )
    copy_elsewhere = await notes_page.move_note_document(
        owner.workspace.id,
        document.id,
        MoveIntoNotebook(notebook_id=elsewhere.id),
        db_session,
        owner.auth,
    )

    assert await db_session.get(Document, document.id) is not None
    # A source of this notebook: the note stands for it ("Added as a source").
    assert into_here.converted_document_id == document.id
    # A source of another notebook only: a copy, the source untouched.
    assert copy_elsewhere.converted_document_id is None


async def test_an_editor_who_may_not_delete_documents_gets_a_copy(
    db_session, make_owner, make_notebook, make_note_document, make_user, add_member
):
    """Moving deletes the document, and the note can then be deleted with
    notebook rights alone; so for an Editor ("Documents (no delete)") the
    move is a copy and the document stays."""
    from app.auth.context import AuthContext

    owner = await make_owner()
    editor = await make_user()
    await add_member(owner.workspace, editor, "Editor")
    editor_auth = AuthContext.session(editor)
    notebook = await make_notebook(owner.workspace.id, "Email")
    document = await make_note_document(
        owner.workspace.id, "Summary.md", "# Mail", created_by_id=owner.user.id
    )
    document_id = document.id

    note = await notes_page.move_note_document(
        owner.workspace.id,
        document_id,
        MoveIntoNotebook(notebook_id=notebook.id),
        db_session,
        editor_auth,
    )

    assert (note.notebook_id, note.content) == (notebook.id, "# Mail")
    assert note.converted_document_id is None
    assert await db_session.get(Document, document_id) is not None
    # Deleting the note (notebook rights) leaves the document where it was.
    await notebook_notes.delete_note(
        owner.workspace.id, notebook.id, note.id, db_session, editor_auth
    )
    assert await db_session.get(Document, document_id) is not None
    # And the Editor still cannot delete the document itself.
    from app.routes import notes_routes

    assert (
        await _status(
            notes_routes.delete_note(
                owner.workspace.id, document_id, db_session, editor_auth
            )
        )
        == 403
    )


async def test_a_viewer_cannot_move_a_note(
    db_session, make_owner, make_notebook, make_note_document, make_user, add_member
):
    from app.auth.context import AuthContext

    owner = await make_owner()
    viewer = await make_user()
    await add_member(owner.workspace, viewer, "Viewer")
    notebook = await make_notebook(owner.workspace.id)
    document = await make_note_document(owner.workspace.id)

    assert (
        await _status(
            notes_page.move_note_document(
                owner.workspace.id,
                document.id,
                MoveIntoNotebook(notebook_id=notebook.id),
                db_session,
                AuthContext.session(viewer),
            )
        )
        == 403
    )
    assert await db_session.get(Document, document.id) is not None


async def test_moves_stay_inside_the_workspace(
    db_session, make_owner, make_notebook, make_note_document, make_document
):
    owner = await make_owner()
    other = await make_owner()
    mine = await make_notebook(owner.workspace.id)
    theirs = await make_notebook(other.workspace.id)
    note_document = await make_note_document(owner.workspace.id)
    a_file = await make_document(owner.workspace.id, "Report.pdf")

    async def move(document_id, notebook_id):
        return await notes_page.move_note_document(
            owner.workspace.id,
            document_id,
            MoveIntoNotebook(notebook_id=notebook_id),
            db_session,
            owner.auth,
        )

    assert await _status(move(note_document.id, theirs.id)) == 404
    assert await _status(move(a_file.id, mine.id)) == 404  # not a note
    assert await _status(move(999_999, mine.id)) == 404


async def test_a_note_still_processing_is_not_moved(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    document = await make_note_document(owner.workspace.id)
    document.status = {"state": "processing"}
    await db_session.flush()
    assert (
        await _status(
            notes_page.move_note_document(
                owner.workspace.id,
                document.id,
                MoveIntoNotebook(notebook_id=notebook.id),
                db_session,
                owner.auth,
            )
        )
        == 409
    )


async def test_a_notebook_note_opens_and_copies_into_another_notebook(
    db_session, make_owner, make_notebook
):
    owner = await make_owner()
    other = await make_owner()
    first = await make_notebook(owner.workspace.id, "First")
    second = await make_notebook(owner.workspace.id, "Second")
    foreign = await make_notebook(other.workspace.id, "Foreign")
    note = await notebook_notes.create_note(
        owner.workspace.id,
        first.id,
        NoteCreate(title="Answer", content="Iron matters.", origin="chat"),
        db_session,
        owner.auth,
    )
    assert note.origin == "chat"

    opened = await notebook_notes.get_note(
        owner.workspace.id, first.id, note.id, Response(), db_session, owner.auth
    )
    assert opened.content == "Iron matters."

    copy = await notebook_notes.copy_note(
        owner.workspace.id,
        first.id,
        note.id,
        NoteCopy(notebook_id=second.id),
        db_session,
        owner.auth,
    )
    assert (copy.notebook_id, copy.title, copy.content, copy.origin) == (
        second.id,
        "Answer",
        "Iron matters.",
        "chat",
    )
    assert (
        await _status(
            notebook_notes.copy_note(
                owner.workspace.id,
                first.id,
                note.id,
                NoteCopy(notebook_id=foreign.id),
                db_session,
                owner.auth,
            )
        )
        == 404
    )
    originals = (
        await db_session.scalars(
            select(NotebookNote).where(NotebookNote.notebook_id == first.id)
        )
    ).all()
    assert [n.id for n in originals] == [note.id]
