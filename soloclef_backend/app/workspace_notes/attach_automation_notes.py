"""One-off: put the notes automations already wrote where the owner looks.

Before the Notes page, an automation told to "save the summary as a note"
with no notebook to save into wrote a Markdown file in ``/documents`` (for
example ``/documents/notes/Email_Summary_2026-10-09.md``) that no Studio
panel shows. And notes saved by "Save results to notebook" did not record
which automation wrote them. For each automation this:

1. finds the note documents stamped with it (runs stamp the files their own
   agent writes, ``automation_stamp``) whose run, if named, is one of its
   runs;
2. lists the unstamped note documents its owner created in its workspace
   between one of its runs' start and end (a run with no end counts until
   its timeout), as **matched by time only**: a chat note or another
   automation's file written in those minutes matches too, so nothing is
   done with one unless it is named in ``document_ids`` (the script's
   ``--document``). A match that also falls in another automation's run says
   so. Named ones are stamped with the automation and the run they fell in;
3. moves the stamped and named documents into the automation's notebook,
   the one picked under "Save results to notebook" (or ``notebook_id`` when
   given), with the same move the Notes page makes; a document a notebook
   uses as a source is left where it is and reported;
4. labels the notebook notes "Save results to notebook" already wrote (title
   "<automation name> — YYYY-MM-DD" in a notebook a run saved into).

Safe to re-run: moved documents are gone, labels are kept, and nothing is
labelled twice. Without ``apply`` nothing is written; the report says what
would be. Run through ``scripts/attach_automation_notes.py``.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime, timedelta

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun
from app.db import Document, DocumentType
from app.notebooks.persistence import Notebook, NotebookNote

from .automation_stamp import note_stamp
from .moving import move_into_notebook
from .note_documents import AUTOMATION_RUN_KEY, automation_id_of
from .source_usage import notebooks_using

# A run that never recorded its end (killed at its timeout) is searched up to
# its timeout; this is the executor's default when the definition sets none.
_DEFAULT_RUN_TIMEOUT_SECONDS = 600
# The date "Save results to notebook" puts after the automation's name.
_TITLE_DATE = re.compile(r"\d{4}-\d{2}-\d{2}")


@dataclass
class AttachReport:
    automation_id: int
    automation_name: str
    workspace_id: int
    notebook_id: int | None = None
    notebook_title: str | None = None
    # (id, title, why): unstamped documents its owner wrote during one of its
    # runs. Nothing is done with one unless it is named.
    time_matches: list[tuple[int, str, str]] = field(default_factory=list)
    # Named time matches, stamped with the automation.
    labelled: list[tuple[int, str]] = field(default_factory=list)
    moved: list[tuple[int, str]] = field(default_factory=list)
    left: list[tuple[int, str, str]] = field(default_factory=list)
    # Named ids that are not one of its time matches: nothing was done.
    not_matched: list[int] = field(default_factory=list)
    notebook_notes_labelled: list[tuple[int, str]] = field(default_factory=list)
    problem: str | None = None


def _save_targets(definition: dict | None) -> list[int]:
    """Every ``save_to_notebook_id`` the definition's steps name, in order."""
    targets: list[int] = []
    for step in (definition or {}).get("plan") or []:
        params = step.get("params") if isinstance(step, dict) else None
        value = (params or {}).get("save_to_notebook_id")
        is_id = isinstance(value, int) and not isinstance(value, bool) and value > 0
        if is_id and value not in targets:
            targets.append(value)
    return targets


def _run_window(run: AutomationRun) -> tuple[datetime, datetime]:
    start = run.started_at or run.created_at
    if run.finished_at is not None:
        return start, run.finished_at
    execution = (run.definition_snapshot or {}).get("execution") or {}
    timeout = execution.get("timeout_seconds")
    if not isinstance(timeout, int) or timeout <= 0:
        timeout = _DEFAULT_RUN_TIMEOUT_SECONDS
    return start, start + timedelta(seconds=timeout)


def _run_during(runs: list[AutomationRun], when: datetime) -> AutomationRun | None:
    for run in runs:
        start, end = _run_window(run)
        if start <= when <= end:
            return run
    return None


async def _other_runs(
    session: AsyncSession, automation: Automation
) -> list[tuple[Automation, list[AutomationRun]]]:
    """The same owner's other automations in the workspace, with their runs."""
    others = (
        await session.scalars(
            select(Automation)
            .where(
                Automation.workspace_id == automation.workspace_id,
                Automation.created_by_user_id == automation.created_by_user_id,
                Automation.id != automation.id,
            )
            .order_by(Automation.id)
        )
    ).all()
    return [
        (
            other,
            list(
                (
                    await session.scalars(
                        select(AutomationRun).where(
                            AutomationRun.automation_id == other.id
                        )
                    )
                ).all()
            ),
        )
        for other in others
    ]


async def _documents_of(
    session: AsyncSession, automation: Automation, runs: list[AutomationRun]
) -> tuple[
    list[Document], list[tuple[Document, AutomationRun]], list[tuple[Document, str]]
]:
    """``(stamped with it, unstamped during one of its runs, wrongly stamped)``."""
    notes = (
        await session.scalars(
            select(Document)
            .where(
                Document.workspace_id == automation.workspace_id,
                Document.document_type == DocumentType.NOTE,
            )
            .order_by(Document.created_at, Document.id)
        )
    ).all()
    run_ids = {run.id for run in runs}
    stamped: list[Document] = []
    matches: list[tuple[Document, AutomationRun]] = []
    wrong: list[tuple[Document, str]] = []
    for document in notes:
        meta = document.document_metadata or {}
        owner = automation_id_of(meta)
        if owner == automation.id:
            run_id = meta.get(AUTOMATION_RUN_KEY)
            if run_id is None or run_id in run_ids:
                stamped.append(document)
            else:
                wrong.append(
                    (
                        document,
                        f"its stamp names run {run_id}, not one of this automation's",
                    )
                )
            continue
        if (
            owner is None
            and automation.created_by_user_id is not None
            and document.created_by_id == automation.created_by_user_id
        ):
            run = _run_during(runs, document.created_at)
            if run is not None:
                matches.append((document, run))
    return stamped, matches, wrong


async def _unlabelled_saved_notes(
    session: AsyncSession, automation: Automation, notebook_ids: list[int]
) -> list[NotebookNote]:
    if not notebook_ids:
        return []
    prefix = f"{automation.name} — "
    candidates = (
        await session.scalars(
            select(NotebookNote)
            .join(Notebook, Notebook.id == NotebookNote.notebook_id)
            .where(
                Notebook.workspace_id == automation.workspace_id,
                NotebookNote.notebook_id.in_(notebook_ids),
                NotebookNote.automation_id.is_(None),
                NotebookNote.origin != "automation",
            )
            .order_by(NotebookNote.id)
        )
    ).all()
    return [
        note
        for note in candidates
        if note.title.startswith(prefix)
        and _TITLE_DATE.fullmatch(note.title[len(prefix) :])
    ]


async def attach_automation_notes(
    session: AsyncSession,
    *,
    automation_ids: list[int] | None = None,
    notebook_id: int | None = None,
    document_ids: list[int] | None = None,
    apply: bool = False,
) -> list[AttachReport]:
    """Report (and with ``apply``, do) the steps above for each automation.

    ``notebook_id`` overrides the automation's own "Save results to
    notebook", and ``document_ids`` names the time matches to take; both
    need exactly one automation, so neither is applied to every automation
    at once.
    """
    if (notebook_id is not None or document_ids) and len(automation_ids or []) != 1:
        raise ValueError(
            "A notebook or documents can only be given for one named automation."
        )
    named = set(document_ids or [])
    stmt = select(Automation).order_by(Automation.id)
    if automation_ids:
        stmt = stmt.where(Automation.id.in_(automation_ids))
    automations = (await session.scalars(stmt)).all()

    reports: list[AttachReport] = []
    for automation in automations:
        report = AttachReport(
            automation_id=automation.id,
            automation_name=automation.name,
            workspace_id=automation.workspace_id,
        )
        reports.append(report)
        runs = list(
            (
                await session.scalars(
                    select(AutomationRun)
                    .where(AutomationRun.automation_id == automation.id)
                    .order_by(AutomationRun.id)
                )
            ).all()
        )

        target_id = notebook_id
        if target_id is None:
            current = _save_targets(automation.definition)
            target_id = current[0] if current else None
        notebook = None
        if target_id is not None:
            notebook = await session.scalar(
                select(Notebook).where(
                    Notebook.id == target_id,
                    Notebook.workspace_id == automation.workspace_id,
                )
            )
            if notebook is None:
                report.problem = (
                    f"notebook {target_id} is not in workspace "
                    f"{automation.workspace_id}; nothing moved"
                )
        else:
            report.problem = (
                "no notebook to move its notes into: pick one under 'Save "
                "results to notebook' in the automation, or pass --notebook"
            )
        if notebook is not None:
            report.notebook_id, report.notebook_title = notebook.id, notebook.title

        # Notes "Save results to notebook" wrote before notes said who wrote them.
        saved_into: list[int] = _save_targets(automation.definition)
        for run in runs:
            for target in _save_targets(run.definition_snapshot):
                if target not in saved_into:
                    saved_into.append(target)
        for note in await _unlabelled_saved_notes(session, automation, saved_into):
            report.notebook_notes_labelled.append((note.id, note.title))
            if apply:
                note.origin = "automation"
                note.automation_id = automation.id

        stamped, matches, wrong = await _documents_of(session, automation, runs)
        report.left.extend((d.id, d.title, why) for d, why in wrong)
        others = await _other_runs(session, automation) if matches else []
        taken: list[Document] = []
        for document, run in matches:
            if document.id in named:
                taken.append(document)
                report.labelled.append((document.id, document.title))
                if apply:
                    meta = dict(document.document_metadata or {})
                    meta.update(
                        note_stamp(
                            automation_id=automation.id,
                            automation_name=automation.name,
                            run_id=run.id,
                        )
                    )
                    document.document_metadata = meta
                continue
            why = f"written by its owner during run {run.id}"
            also = [
                f"{other.id} {other.name!r}"
                for other, other_runs in others
                if _run_during(other_runs, document.created_at) is not None
            ]
            if also:
                why += "; also during a run of automation " + ", ".join(also)
            report.time_matches.append((document.id, document.title, why))
        matched_ids = {document.id for document, _ in matches}
        report.not_matched = sorted(named - matched_ids)
        if apply:
            await session.commit()

        documents = sorted(stamped + taken, key=lambda d: (d.created_at, d.id))
        used = await notebooks_using(
            session, automation.workspace_id, [(d.id, d.folder_id) for d in documents]
        )
        for document in documents:
            if document.id in used:
                names = ", ".join(ref.title for ref in used[document.id])
                report.left.append(
                    (document.id, document.title, f"a source in {names}")
                )
                continue
            if notebook is None:
                continue
            report.moved.append((document.id, document.title))
            if apply:
                await move_into_notebook(
                    session,
                    document=document,
                    notebook=notebook,
                    moved_by=automation.created_by_user_id,
                )
        if apply:
            await session.commit()
    return reports
