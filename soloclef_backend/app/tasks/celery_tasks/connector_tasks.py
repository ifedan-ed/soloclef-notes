"""Celery tasks for connector indexing."""

import contextlib
import logging
import time
from collections.abc import Awaitable, Callable

from celery import current_task

from app.celery_app import celery_app
from app.observability.analytics import posthog as ph_analytics
from app.observability.core.errors import categorize_exception
from app.observability.domains import indexing
from app.tasks.celery_tasks import (
    get_celery_session_maker,
    run_async_celery_task as _run_async_celery_task,
)

logger = logging.getLogger(__name__)


def run_async_celery_task[T](
    coro_factory: Callable[[], Awaitable[T]],
    *,
    workspace_id: int | None = None,
    user_id: str | None = None,
) -> T:
    """Run connector sync work and record aggregate connector metrics.

    When ``workspace_id``/``user_id`` are provided, also emits the PostHog
    ``connector_indexing_completed``/``_failed`` outcome (the frontend only
    knows indexing *started*, never whether it worked). No-op without a key.
    """
    task_name = getattr(current_task, "name", None) or "unknown"
    t0 = time.perf_counter()
    status = "failed"
    error_category: str | None = None
    try:
        with indexing.connector_sync_span(connector_type=task_name) as sp:
            try:
                result = _run_async_celery_task(coro_factory)
                sp.set_attribute("connector.status", "success")
            except Exception as exc:
                error_category = categorize_exception(exc)
                sp.set_attribute("connector.error.category", error_category)
                raise
        status = "success"
        return result
    finally:
        elapsed_s = time.perf_counter() - t0
        indexing.record_connector_sync_duration(
            elapsed_s,
            connector_type=task_name,
        )
        indexing.record_connector_sync_outcome(
            connector_type=task_name,
            status=status,
            error_category=error_category,
        )
        if user_id and ph_analytics.is_enabled():
            event = (
                "connector_indexing_completed"
                if status == "success"
                else "connector_indexing_failed"
            )
            with contextlib.suppress(Exception):
                ph_analytics.capture(
                    event,
                    distinct_id=str(user_id),
                    properties={
                        "workspace_id": workspace_id,
                        # ``connector_type`` is the Celery task name, e.g.
                        # index_google_drive_files.
                        "connector_type": task_name,
                        "status": status,
                        "error_category": error_category,
                        "duration_ms": int(elapsed_s * 1000),
                    },
                    groups={"workspace": str(workspace_id)}
                    if workspace_id is not None
                    else None,
                )


@celery_app.task(name="index_google_drive_files", bind=True)
def index_google_drive_files_task(
    self,
    connector_id: int,
    workspace_id: int,
    user_id: str,
    items_dict: dict,  # Dictionary with 'folders', 'files', and 'indexing_options'
):
    """Celery task to index Google Drive folders and files."""
    return run_async_celery_task(
        lambda: _index_google_drive_files(
            connector_id,
            workspace_id,
            user_id,
            items_dict,
        ),
        workspace_id=workspace_id,
        user_id=user_id,
    )


async def _index_google_drive_files(
    connector_id: int,
    workspace_id: int,
    user_id: str,
    items_dict: dict,  # Dictionary with 'folders', 'files', and 'indexing_options'
):
    """Index Google Drive folders and files with new session."""
    from app.routes.search_source_connectors_routes import (
        run_google_drive_indexing,
    )

    async with get_celery_session_maker()() as session:
        await run_google_drive_indexing(
            session,
            connector_id,
            workspace_id,
            user_id,
            items_dict,
        )
