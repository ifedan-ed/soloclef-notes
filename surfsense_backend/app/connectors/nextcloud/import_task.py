"""Celery task that downloads and parses the files of one Nextcloud import.

Runs once per import; there is no schedule and nothing re-runs it later.
"""

from __future__ import annotations

import logging

from app.celery_app import celery_app
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

from .import_job import import_document

logger = logging.getLogger(__name__)


@celery_app.task(name="nextcloud_import_documents", bind=True)
def import_nextcloud_documents_task(
    self, document_ids: list[int], user_id: str, max_bytes: int
):
    return run_async_celery_task(
        lambda: import_documents(document_ids, user_id, max_bytes)
    )


async def import_documents(
    document_ids: list[int], user_id: str, max_bytes: int
) -> dict[str, int]:
    """Each file on its own; one failure never strands the rest."""
    outcomes: dict[str, int] = {}
    async with get_celery_session_maker()() as session:
        for document_id in document_ids:
            try:
                outcome = await import_document(
                    session, document_id, user_id, max_bytes=max_bytes
                )
            except Exception:
                logger.exception("Nextcloud import of document %s failed", document_id)
                await session.rollback()
                outcome = "failed"
            outcomes[outcome] = outcomes.get(outcome, 0) + 1
    return outcomes
