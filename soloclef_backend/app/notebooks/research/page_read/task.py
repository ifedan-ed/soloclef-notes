"""Celery task that reads pasted pages in the background (Browserbase first)."""

from __future__ import annotations

import logging
from uuid import UUID

from app.celery_app import celery_app
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

from ..capability_runner import CapabilityRunner
from ..import_job import fill_document
from .reader import read_web_page

logger = logging.getLogger(__name__)

TASK_NAME = "notebook_read_page"


@celery_app.task(name=TASK_NAME, bind=True)
def read_pages_task(self, document_ids: list[int], workspace_id: int, user_id: str):
    return run_async_celery_task(
        lambda: fill_pages(document_ids, workspace_id, user_id)
    )


async def fill_pages(document_ids: list[int], workspace_id: int, user_id: str) -> None:
    """Fill each pasted page on its own; one failure never strands the rest."""
    async with get_celery_session_maker()() as session:
        # Charge, don't gate: the person already asked for the page.
        runner = CapabilityRunner(session, workspace_id, gate=False)
        for document_id in document_ids:
            try:
                await fill_document(
                    session, document_id, UUID(user_id), runner, fetch=read_web_page
                )
            except Exception:
                logger.exception("reading pasted page %s failed", document_id)
                await session.rollback()
