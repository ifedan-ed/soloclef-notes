"""Which notebooks are about to name themselves, so the apps know to look again.

A notebook still named automatically is "pending" while one of its document
sources is being indexed or became ready moments ago: its name is then
seconds away. The apps poll the notebook (or the home list) only while this
is true, and the window is short, so a naming that fails stops the polling
on its own.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

from sqlalchemy import cast, func, or_, select
from sqlalchemy.dialects.postgresql import TIMESTAMP
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document

from ..persistence import Notebook, NotebookSource
from .policy import MAX_RUNS, REFINE_WINDOW, AutoTitleState

RECENTLY_READY = timedelta(minutes=2)
# A source stuck indexing longer than this no longer keeps anyone polling.
INDEXING_WINDOW = timedelta(minutes=30)
_INDEXING = ("pending", "processing")


async def naming_pending(
    session: AsyncSession, notebooks: list[Notebook], *, now: datetime | None = None
) -> set[int]:
    now = now or datetime.now(UTC)
    candidates = [n.id for n in notebooks if _may_still_rename(n, now)]
    if not candidates:
        return set()
    since = now - RECENTLY_READY
    # A source ready before the last automatic name was already used by it.
    named_at = cast(Notebook.auto_title["at"].astext, TIMESTAMP(timezone=True))
    rows = await session.scalars(
        select(NotebookSource.notebook_id)
        .join(Document, Document.id == NotebookSource.document_id)
        .join(Notebook, Notebook.id == NotebookSource.notebook_id)
        .where(
            NotebookSource.notebook_id.in_(candidates),
            or_(
                (Document.status["state"].astext.in_(_INDEXING))
                & (NotebookSource.added_at >= now - INDEXING_WINDOW),
                Document.updated_at
                >= func.greatest(since, func.coalesce(named_at, since)),
            ),
        )
        .distinct()
    )
    return set(rows.all())


def _may_still_rename(notebook: Notebook, now: datetime) -> bool:
    state = AutoTitleState.of(notebook.title_source, notebook.auto_title)
    if state.title_source != "auto" or state.runs >= MAX_RUNS:
        return False
    if state.runs == 0:
        return True
    return state.at is not None and now - state.at <= REFINE_WINDOW
