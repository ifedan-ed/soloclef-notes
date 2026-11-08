"""Celery task that fills research imports in the background."""

from __future__ import annotations

import logging
from uuid import UUID

from app.celery_app import celery_app
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

from .capability_runner import CapabilityRunner
from .import_job import fill_document

logger = logging.getLogger(__name__)


@celery_app.task(name="notebook_research_import", bind=True)
def import_research_sources_task(
    self, document_ids: list[int], workspace_id: int, user_id: str
):
    return run_async_celery_task(
        lambda: fill_documents(document_ids, workspace_id, user_id)
    )


async def fill_documents(
    document_ids: list[int], workspace_id: int, user_id: str
) -> None:
    """Fill each document on its own; one failure never strands the rest."""
    async with get_celery_session_maker()() as session:
        # Billing never blocks an import the user already made: charge, don't gate.
        runner = CapabilityRunner(session, workspace_id, gate=False)
        for document_id in document_ids:
            try:
                await fill_document(session, document_id, UUID(user_id), runner)
            except Exception:
                logger.exception("research import of document %s failed", document_id)
                await session.rollback()
