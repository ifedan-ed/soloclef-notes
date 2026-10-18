"""Save an ``agent_task`` step's final output as a note in a notebook.

Writes directly to ``notebook_notes`` -- the same table and shape the
notebook UI's own "New note" uses (``app/notebooks/routes/notes.py``) -- so
the result shows up where a person's own notes do, not as a workspace
document only reachable through the @ picker.
"""

from __future__ import annotations

from datetime import UTC, datetime
from uuid import UUID
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.notebooks.access import touch
from app.notebooks.persistence import Notebook, NotebookNote

_MAX_TITLE = 300


def _local_date(timezone: str | None) -> str:
    tz = None
    if timezone:
        try:
            tz = ZoneInfo(timezone)
        except ZoneInfoNotFoundError:
            tz = None
    now = datetime.now(tz) if tz else datetime.now(UTC)
    return now.strftime("%Y-%m-%d")


def note_title(automation_name: str, *, timezone: str | None) -> str:
    title = f"{automation_name} — {_local_date(timezone)}"
    return title[:_MAX_TITLE]


async def load_save_target(
    session: AsyncSession, *, notebook_id: int, workspace_id: int
) -> Notebook:
    """The notebook a step saves into. Raises ``ValueError`` if it isn't one
    of this workspace's (deleted, or never existed).

    Checked before the agent runs as well as when saving, so a step whose
    notebook is gone fails at once instead of after a paid model call.
    """
    notebook = await session.scalar(
        select(Notebook).where(
            Notebook.id == notebook_id, Notebook.workspace_id == workspace_id
        )
    )
    if notebook is None:
        raise ValueError(
            f"The notebook this step saves results to ({notebook_id}) no "
            "longer exists. Edit the automation and pick another."
        )
    return notebook


async def save_run_output_as_note(
    session: AsyncSession,
    *,
    notebook_id: int,
    workspace_id: int,
    automation_name: str,
    content: str,
    timezone: str | None = None,
    created_by_id: UUID | None = None,
    automation_id: int | None = None,
) -> NotebookNote:
    """Insert a note in ``notebook_id``, labelled as this automation's (the
    Notes page shows which automation wrote it). Raises ``ValueError`` if
    the notebook isn't one of this workspace's."""
    notebook = await load_save_target(
        session, notebook_id=notebook_id, workspace_id=workspace_id
    )
    note = NotebookNote(
        notebook_id=notebook.id,
        title=note_title(automation_name, timezone=timezone),
        content=content,
        created_by_id=created_by_id,
        origin="automation",
        automation_id=automation_id,
    )
    session.add(note)
    touch(notebook)
    return note
