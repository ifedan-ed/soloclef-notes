"""The one-off that puts notes automations wrote as files into their notebooks.

Files a run stamped are its own; older, unstamped files that only fall in a
run's time are listed and taken only when named."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from unittest.mock import AsyncMock

import pytest
from sqlalchemy import select

from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun
from app.db import Document
from app.notebooks.persistence import NotebookNote
from app.workspace_notes import moving
from app.workspace_notes.attach_automation_notes import attach_automation_notes
from app.workspace_notes.automation_stamp import note_stamp

pytestmark = pytest.mark.integration

_NOON = datetime(2026, 10, 8, 12, 0, tzinfo=UTC)
_DAY = timedelta(days=1)


@pytest.fixture(autouse=True)
def _no_git_store(monkeypatch):
    monkeypatch.setattr(
        moving.knowledge_store, "record_deleted_documents", AsyncMock(return_value=None)
    )


async def _automation(
    db_session,
    owner,
    *,
    save_to: int | None = None,
    name: str = "Daily unread emails summary",
):
    params = {"query": "Summarize unread mail, then save it as a note."}
    if save_to is not None:
        params["save_to_notebook_id"] = save_to
    definition = {
        "schema_version": "1.0",
        "name": name,
        "plan": [{"step_id": "step_1", "action": "agent_task", "params": params}],
    }
    automation = Automation(
        workspace_id=owner.workspace.id,
        created_by_user_id=owner.user.id,
        name=name,
        definition=definition,
    )
    db_session.add(automation)
    await db_session.flush()
    return automation


async def _run(db_session, automation, start: datetime, minutes: int | None = 3):
    run = AutomationRun(
        automation_id=automation.id,
        status=RunStatus.SUCCEEDED,
        definition_snapshot=automation.definition,
        inputs={},
        started_at=start,
        finished_at=start + timedelta(minutes=minutes) if minutes else None,
        created_at=start,
    )
    db_session.add(run)
    await db_session.flush()
    return run


async def _notes_in(db_session, notebook_id):
    return (
        await db_session.scalars(
            select(NotebookNote)
            .where(NotebookNote.notebook_id == notebook_id)
            .order_by(NotebookNote.created_at)
        )
    ).all()


def _stamped(automation, run) -> dict:
    """What a run's agent now writes into the metadata of its note files."""
    return {
        "virtual_path": "/documents/notes/x.md",
        **note_stamp(
            automation_id=automation.id,
            automation_name=automation.name,
            run_id=run.id,
        ),
    }


async def test_report_only_changes_nothing_and_apply_moves_the_runs_stamped_notes(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    stranger = await make_owner()
    notebook = await make_notebook(owner.workspace.id, "Email")
    automation = await _automation(db_session, owner, save_to=notebook.id)
    monday = await _run(db_session, automation, _NOON - _DAY)
    tuesday = await _run(db_session, automation, _NOON, minutes=None)

    first = await make_note_document(
        owner.workspace.id,
        "Email_Summary_2026-10-07.md",
        "Monday's mail",
        created_by_id=owner.user.id,
        created_at=_NOON - _DAY + timedelta(minutes=1),
        metadata=_stamped(automation, monday),
    )
    second = await make_note_document(
        owner.workspace.id,
        "email-summary-2026-10-08.md",
        "Tuesday's mail",
        created_by_id=owner.user.id,
        created_at=_NOON + timedelta(minutes=7),
        metadata=_stamped(automation, tuesday),
    )
    by_hand_later = await make_note_document(
        owner.workspace.id,
        "Shopping.md",
        created_by_id=owner.user.id,
        created_at=_NOON + 2 * _DAY,
    )
    # Someone else's note in another workspace at the same minute.
    await make_note_document(
        stranger.workspace.id,
        "Theirs.md",
        created_by_id=stranger.user.id,
        created_at=_NOON + timedelta(minutes=1),
    )
    first_id, second_id = first.id, second.id

    [report] = await attach_automation_notes(db_session, automation_ids=[automation.id])
    assert report.notebook_id == notebook.id
    assert [doc_id for doc_id, _ in report.moved] == [first_id, second_id]
    assert report.time_matches == []
    assert await _notes_in(db_session, notebook.id) == []

    [report] = await attach_automation_notes(
        db_session, automation_ids=[automation.id], apply=True
    )

    notes = await _notes_in(db_session, notebook.id)
    assert [(n.title, n.content) for n in notes] == [
        ("Email_Summary_2026-10-07", "Monday's mail"),
        ("email-summary-2026-10-08", "Tuesday's mail"),
    ]
    assert {(n.origin, n.automation_id) for n in notes} == {
        ("automation", automation.id)
    }
    assert await db_session.get(Document, first_id) is None
    assert await db_session.get(Document, second_id) is None
    assert await db_session.get(Document, by_hand_later.id) is not None

    # Running it again finds nothing more to do.
    [again] = await attach_automation_notes(
        db_session, automation_ids=[automation.id], apply=True
    )
    assert (again.moved, again.labelled, again.notebook_notes_labelled) == ([], [], [])
    assert len(await _notes_in(db_session, notebook.id)) == 2


async def test_an_unstamped_note_written_during_a_run_moves_only_when_named(
    db_session, make_owner, make_notebook, make_note_document
):
    """Notes from before the stamp: the time says only that it might be the
    run's; a chat note written in those minutes looks the same."""
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id, "Email")
    automation = await _automation(db_session, owner, save_to=notebook.id)
    run = await _run(db_session, automation, _NOON, minutes=None)  # killed
    summary = await make_note_document(
        owner.workspace.id,
        "Email_Summary_2026-10-08.md",
        created_by_id=owner.user.id,
        created_at=_NOON + timedelta(minutes=7),  # within the 600 s timeout
    )
    chat_note = await make_note_document(
        owner.workspace.id,
        "My plan.md",
        created_by_id=owner.user.id,
        created_at=_NOON + timedelta(minutes=2),
    )
    summary_id, chat_id = summary.id, chat_note.id

    [report] = await attach_automation_notes(
        db_session, automation_ids=[automation.id], apply=True
    )
    assert [doc_id for doc_id, _, _ in report.time_matches] == [chat_id, summary_id]
    assert f"during run {run.id}" in report.time_matches[0][2]
    assert report.moved == []
    assert await _notes_in(db_session, notebook.id) == []
    assert "automation_id" not in chat_note.document_metadata

    [report] = await attach_automation_notes(
        db_session,
        automation_ids=[automation.id],
        document_ids=[summary_id, 999_999],
        apply=True,
    )
    assert [doc_id for doc_id, _ in report.labelled] == [summary_id]
    assert [doc_id for doc_id, _ in report.moved] == [summary_id]
    assert report.not_matched == [999_999]
    [note] = await _notes_in(db_session, notebook.id)
    assert (note.title, note.automation_id) == (
        "Email_Summary_2026-10-08",
        automation.id,
    )
    assert await db_session.get(Document, summary_id) is None
    # The chat note was never named, so it is where it was, unlabelled.
    await db_session.refresh(chat_note)
    assert chat_note.id == chat_id
    assert "automation_id" not in chat_note.document_metadata


async def test_overlapping_runs_never_take_each_others_notes_or_a_chat_note(
    db_session, make_owner, make_notebook, make_note_document
):
    """Two automations on the same schedule, and a chat note in the same
    minutes: each automation gets only the file its own run stamped."""
    owner = await make_owner()
    mail_notebook = await make_notebook(owner.workspace.id, "Mail")
    news_notebook = await make_notebook(owner.workspace.id, "News")
    mail = await _automation(db_session, owner, save_to=mail_notebook.id, name="Mail")
    news = await _automation(db_session, owner, save_to=news_notebook.id, name="News")
    mail_run = await _run(db_session, mail, _NOON)
    news_run = await _run(db_session, news, _NOON)
    mail_file = await make_note_document(
        owner.workspace.id,
        "Mail_2026-10-08.md",
        created_by_id=owner.user.id,
        created_at=_NOON + timedelta(minutes=1),
        metadata=_stamped(mail, mail_run),
    )
    news_file = await make_note_document(
        owner.workspace.id,
        "News_2026-10-08.md",
        created_by_id=owner.user.id,
        created_at=_NOON + timedelta(minutes=2),
        metadata=_stamped(news, news_run),
    )
    chat_note = await make_note_document(
        owner.workspace.id,
        "My chat plan.md",
        created_by_id=owner.user.id,
        created_at=_NOON + timedelta(minutes=1),
    )
    chat_id = chat_note.id

    reports = await attach_automation_notes(db_session, apply=True)

    by_id = {report.automation_id: report for report in reports}
    assert [doc_id for doc_id, _ in by_id[news.id].moved] == [news_file.id]
    assert [doc_id for doc_id, _ in by_id[mail.id].moved] == [mail_file.id]
    assert [n.title for n in await _notes_in(db_session, mail_notebook.id)] == [
        "Mail_2026-10-08"
    ]
    assert [n.title for n in await _notes_in(db_session, news_notebook.id)] == [
        "News_2026-10-08"
    ]
    # The chat note is listed for both as a time match, and says so.
    [(doc_id, _, why)] = by_id[mail.id].time_matches
    assert doc_id == chat_id
    assert f"also during a run of automation {news.id} 'News'" in why
    assert await db_session.get(Document, chat_id) is not None


async def test_a_stamp_naming_another_automations_run_is_left(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id, "Email")
    automation = await _automation(db_session, owner, save_to=notebook.id)
    other = await _automation(db_session, owner, name="Other")
    others_run = await _run(db_session, other, _NOON)
    await _run(db_session, automation, _NOON - _DAY)
    odd = await make_note_document(
        owner.workspace.id,
        "Odd.md",
        created_by_id=owner.user.id,
        metadata={
            "virtual_path": "/documents/Odd.md",
            "automation_id": automation.id,
            "automation_run_id": others_run.id,
        },
    )

    [report] = await attach_automation_notes(
        db_session, automation_ids=[automation.id], apply=True
    )

    assert report.moved == []
    [(doc_id, _, why)] = report.left
    assert doc_id == odd.id
    assert f"run {others_run.id}" in why
    assert await db_session.get(Document, odd.id) is not None


async def test_without_a_notebook_the_named_notes_are_labelled_and_left(
    db_session, make_owner, make_notebook, make_note_document
):
    owner = await make_owner()
    automation = await _automation(db_session, owner)
    run = await _run(db_session, automation, _NOON)
    document = await make_note_document(
        owner.workspace.id,
        "Email_Summary_2026-10-08.md",
        created_by_id=owner.user.id,
        created_at=_NOON + timedelta(minutes=1),
    )

    [report] = await attach_automation_notes(
        db_session,
        automation_ids=[automation.id],
        document_ids=[document.id],
        apply=True,
    )

    assert "Save results to notebook" in report.problem
    assert report.moved == []
    await db_session.refresh(document)
    assert document.document_metadata["automation_id"] == automation.id
    assert document.document_metadata["automation_run_id"] == run.id

    # Named on the command line instead, the now stamped note moves.
    notebook = await make_notebook(owner.workspace.id, "Email")
    [report] = await attach_automation_notes(
        db_session,
        automation_ids=[automation.id],
        notebook_id=notebook.id,
        apply=True,
    )
    assert [doc_id for doc_id, _ in report.moved] == [document.id]
    assert len(await _notes_in(db_session, notebook.id)) == 1


async def test_a_notebook_or_documents_need_one_named_automation(db_session):
    with pytest.raises(ValueError):
        await attach_automation_notes(db_session, notebook_id=1)
    with pytest.raises(ValueError):
        await attach_automation_notes(db_session, document_ids=[1])
    with pytest.raises(ValueError):
        await attach_automation_notes(
            db_session, automation_ids=[1, 2], document_ids=[1]
        )


async def test_notes_save_to_notebook_already_wrote_get_their_label(
    db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id, "Email")
    automation = await _automation(db_session, owner, save_to=notebook.id)
    saved = NotebookNote(
        notebook_id=notebook.id,
        title="Daily unread emails summary — 2026-10-01",
        content="Mail.",
    )
    unrelated = NotebookNote(
        notebook_id=notebook.id, title="Daily unread emails summary — draft", content=""
    )
    db_session.add_all([saved, unrelated])
    await db_session.flush()

    [report] = await attach_automation_notes(
        db_session, automation_ids=[automation.id], apply=True
    )

    assert [note_id for note_id, _ in report.notebook_notes_labelled] == [saved.id]
    await db_session.refresh(saved)
    await db_session.refresh(unrelated)
    assert (saved.origin, saved.automation_id) == ("automation", automation.id)
    assert (unrelated.origin, unrelated.automation_id) == ("hand", None)
