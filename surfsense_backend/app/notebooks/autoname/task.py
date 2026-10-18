"""The background task that names a notebook, and the hooks that queue it.

Queued, never awaited: an upload or a chat turn never waits on a title, and
a failure is logged and forgotten (the notebook keeps its title).

- ``after_document_ready``: the indexing pipeline calls it when a document
  finishes indexing; every auto-named notebook linking it is queued.
- ``after_sources_added``: adding already-ready documents to a notebook.
- ``after_question``: a notebook chat's turn starts; the question names a
  notebook that has no ready source yet.
"""

from __future__ import annotations

import logging

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.celery_app import celery_app
from app.db import Document
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

from ..persistence import Notebook
from .policy import AutoTitleState
from .service import autoname_notebook, notebooks_to_name

logger = logging.getLogger(__name__)

# A batch of uploads finishes within seconds of each other; waiting a moment
# lets the first name see more than the very first file.
SOURCE_SETTLE_SECONDS = 4


@celery_app.task(name="notebook_autoname", ignore_result=True)
def autoname_notebook_task(
    notebook_id: int, trigger: str = "source", question: str | None = None
):
    return run_async_celery_task(lambda: _run(notebook_id, trigger, question))


async def _run(notebook_id: int, trigger: str, question: str | None) -> None:
    try:
        async with get_celery_session_maker()() as session:
            await autoname_notebook(
                session,
                notebook_id,
                trigger=trigger,  # type: ignore[arg-type]
                question=question,
            )
    except Exception:
        logger.warning(
            "Automatic naming of notebook %s failed", notebook_id, exc_info=True
        )


def queue_autoname(
    notebook_id: int,
    *,
    trigger: str,
    question: str | None = None,
    countdown: int = 0,
) -> None:
    try:
        autoname_notebook_task.apply_async(
            args=[notebook_id, trigger, question], countdown=countdown
        )
    except Exception:
        logger.warning(
            "Could not queue automatic naming of notebook %s",
            notebook_id,
            exc_info=True,
        )


async def after_document_ready(session: AsyncSession, document_id: int) -> None:
    try:
        notebook_ids = await notebooks_to_name(session, document_id)
    except Exception:
        logger.warning(
            "Looking up notebooks to name for document %s failed",
            document_id,
            exc_info=True,
        )
        await session.rollback()
        return
    for notebook_id in notebook_ids:
        queue_autoname(notebook_id, trigger="source", countdown=SOURCE_SETTLE_SECONDS)


async def after_sources_added(
    session: AsyncSession, notebook_id: int, title_source: str, document_ids: list[int]
) -> None:
    if title_source != "auto" or not document_ids:
        return
    ready = await session.scalar(
        select(Document.id)
        .where(
            Document.id.in_(document_ids),
            Document.status["state"].astext == "ready",
        )
        .limit(1)
    )
    if ready is not None:
        queue_autoname(notebook_id, trigger="source", countdown=SOURCE_SETTLE_SECONDS)


async def after_question(
    session: AsyncSession, notebook_id: int, question: str | None
) -> None:
    """Queue naming if the notebook has never been named; a cheap read first."""
    if not (question and question.strip()):
        return
    try:
        row = (
            await session.execute(
                select(Notebook.title_source, Notebook.auto_title).where(
                    Notebook.id == notebook_id
                )
            )
        ).first()
    except Exception:
        logger.warning(
            "Reading notebook %s for naming failed", notebook_id, exc_info=True
        )
        return
    if row is None or AutoTitleState.of(row[0], row[1]).runs or row[0] != "auto":
        return
    queue_autoname(notebook_id, trigger="question", question=question[:2000])
