"""Meta-scheduler task that checks for connectors needing periodic indexing.

Periodic indexing settings live on ``SearchSourceConnectorEnablement`` now,
one row per (connector, workspace) — a connector can be enabled, and
indexing, in more than one workspace since it moved to the account
(``custom/ACCOUNT_CONNECTIONS.md``). Each due enablement dispatches its own
indexing run, writing documents into its own ``workspace_id``.

An enablement whose connector is no longer usable in its workspace (the
connector's owner left it, or switched the connector off there) is never
dispatched: its periodic indexing is switched off instead, so a row written
for a workspace the owner was never in, or has left, cannot keep pulling the
owner's files into it.
"""

import logging
from datetime import UTC, datetime

from sqlalchemy import select
from sqlalchemy.orm import joinedload

from app.celery_app import celery_app
from app.db import (
    SearchSourceConnector,
    SearchSourceConnectorEnablement,
    SearchSourceConnectorType,
)
from app.notifications.persistence import Notification
from app.services.connector_enablement import is_enabled_in_workspace
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task
from app.utils.indexing_locks import is_connector_indexing_locked

logger = logging.getLogger(__name__)


@celery_app.task(name="check_periodic_schedules")
def check_periodic_schedules_task():
    """
    Check all connectors for periodic indexing that's due.
    This task runs every minute and triggers indexing for any connector
    whose next_scheduled_at time has passed.
    """
    return run_async_celery_task(_check_and_trigger_schedules)


async def _check_and_trigger_schedules():
    """Check database for enablements that need indexing and trigger their tasks."""
    async with get_celery_session_maker()() as session:
        try:
            now = datetime.now(UTC)
            result = await session.execute(
                select(SearchSourceConnectorEnablement)
                .options(joinedload(SearchSourceConnectorEnablement.connector))
                .filter(
                    SearchSourceConnectorEnablement.periodic_indexing_enabled.is_(True),
                    SearchSourceConnectorEnablement.next_scheduled_at <= now,
                )
            )
            due_enablements = result.scalars().all()

            if not due_enablements:
                logger.debug("No connectors due for periodic indexing")
                return

            logger.info(f"Found {len(due_enablements)} enablements due for indexing")

            from app.tasks.celery_tasks.connector_tasks import (
                index_google_drive_files_task,
            )

            task_map = {
                SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR: index_google_drive_files_task,
            }

            for enablement in due_enablements:
                connector: SearchSourceConnector = enablement.connector
                if not await is_enabled_in_workspace(
                    session, connector, enablement.workspace_id
                ):
                    # The owner isn't a member there (any more), or switched
                    # it off there: never index into that workspace.
                    logger.warning(
                        f"Switching off periodic indexing for connector "
                        f"{connector.id} in workspace {enablement.workspace_id}: "
                        "not usable there (its owner is not a member, or it "
                        "is switched off)"
                    )
                    enablement.periodic_indexing_enabled = False
                    enablement.next_scheduled_at = None
                    await session.commit()
                    continue
                # Primary guard: Redis lock indicates a task is currently running
                # for this connector (locked per connector, not per workspace —
                # fine today since only Google Drive schedules here, and a
                # Drive connector enabled in two workspaces still has one
                # underlying account to rate-limit against).
                if is_connector_indexing_locked(connector.id):
                    logger.info(
                        f"Skipping periodic indexing for connector {connector.id} "
                        f"in workspace {enablement.workspace_id} "
                        "(Redis lock indicates indexing is already in progress)"
                    )
                    continue

                in_progress_result = await session.execute(
                    select(Notification.id).where(
                        Notification.type == "connector_indexing",
                        Notification.notification_metadata["connector_id"].astext
                        == str(connector.id),
                        Notification.notification_metadata["status"].astext
                        == "in_progress",
                    )
                )
                if in_progress_result.first():
                    logger.info(
                        f"Skipping periodic indexing for connector {connector.id} "
                        "(already has in-progress indexing notification)"
                    )
                    continue

                task = task_map.get(connector.connector_type)
                if not task:
                    logger.warning(
                        f"No task found for connector type {connector.connector_type}"
                    )
                    continue

                logger.info(
                    f"Triggering periodic indexing for connector {connector.id} "
                    f"({connector.connector_type.value}) in workspace "
                    f"{enablement.workspace_id}"
                )

                if (
                    connector.connector_type
                    == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR
                ):
                    # Per-workspace selection, if this workspace set one;
                    # otherwise the account default from the connector itself.
                    connector_config = connector.config or {}
                    workspace_config = enablement.indexing_config or {}
                    selected_folders = workspace_config.get(
                        "selected_folders"
                    ) or connector_config.get("selected_folders", [])
                    selected_files = workspace_config.get(
                        "selected_files"
                    ) or connector_config.get("selected_files", [])
                    indexing_options = workspace_config.get(
                        "indexing_options"
                    ) or connector_config.get(
                        "indexing_options",
                        {
                            "max_files_per_folder": 100,
                            "incremental_sync": True,
                            "include_subfolders": True,
                        },
                    )

                    if selected_folders or selected_files:
                        task.delay(
                            connector.id,
                            enablement.workspace_id,
                            str(connector.user_id),
                            {
                                "folders": selected_folders,
                                "files": selected_files,
                                "indexing_options": indexing_options,
                            },
                        )
                    else:
                        # No folders/files selected - skip indexing but still
                        # update next_scheduled_at to prevent checking every minute
                        logger.info(
                            f"Google Drive connector {connector.id} has no folders "
                            f"or files selected for workspace {enablement.workspace_id}, "
                            "skipping periodic indexing (will check again at next scheduled time)"
                        )
                        from datetime import timedelta

                        enablement.next_scheduled_at = now + timedelta(
                            minutes=enablement.indexing_frequency_minutes
                        )
                        await session.commit()
                        continue
                else:
                    task.delay(
                        connector.id,
                        enablement.workspace_id,
                        str(connector.user_id),
                        None,  # start_date - uses last_indexed_at
                        None,  # end_date - uses now
                    )

                from datetime import timedelta

                enablement.next_scheduled_at = now + timedelta(
                    minutes=enablement.indexing_frequency_minutes
                )
                await session.commit()

        except Exception as e:
            logger.error(f"Error checking periodic schedules: {e!s}", exc_info=True)
            await session.rollback()
