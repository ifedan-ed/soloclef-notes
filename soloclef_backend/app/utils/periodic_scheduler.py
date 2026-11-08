"""
Utility functions for managing periodic connector indexing schedules.

This module uses a meta-scheduler pattern instead of RedBeat's dynamic schedule creation.
Instead of creating individual Beat schedules for each connector, we:
1. Store schedule configuration in the database (next_scheduled_at, frequency)
2. Have ONE Beat task that runs every minute checking for due connectors
3. Trigger indexing tasks for connectors whose next_scheduled_at has passed

This avoids RedBeat's limitation where new schedules aren't discovered without restart.
"""

import logging

from app.db import SearchSourceConnectorType

logger = logging.getLogger(__name__)


def create_periodic_schedule(
    connector_id: int,
    workspace_id: int,
    user_id: str,
    connector_type: SearchSourceConnectorType,
    frequency_minutes: int,
    connector_config: dict | None = None,
) -> bool:
    """Record that periodic indexing was enabled for a connector.

    The schedule lives in the database (``next_scheduled_at``) and the
    meta-scheduler task starts the first run when it falls due.
    """
    logger.info(
        f"Periodic indexing enabled for connector {connector_id} "
        f"(frequency: {frequency_minutes} minutes)"
    )
    return True


def delete_periodic_schedule(connector_id: int) -> bool:
    """
    Handle deletion of periodic schedule for a connector.

    Note: With the meta-scheduler pattern, the schedule is managed in the database.
    The next_scheduled_at field being set to None effectively disables it.
    This function just logs the action.

    Args:
        connector_id: ID of the connector

    Returns:
        True (always successful since database handles the state)
    """
    logger.info(f"Periodic indexing disabled for connector {connector_id}")
    return True


def update_periodic_schedule(
    connector_id: int,
    workspace_id: int,
    user_id: str,
    connector_type: SearchSourceConnectorType,
    frequency_minutes: int,
) -> bool:
    """
    Update an existing periodic schedule for a connector.

    Note: With the meta-scheduler pattern, updates are handled by the database.
    This function logs the update and optionally triggers an immediate run.

    Args:
        connector_id: ID of the connector
        workspace_id: ID of the workspace
        user_id: User ID
        connector_type: Type of connector
        frequency_minutes: New frequency in minutes

    Returns:
        True if successful, False otherwise
    """
    logger.info(
        f"Periodic indexing schedule updated for connector {connector_id} "
        f"(new frequency: {frequency_minutes} minutes)"
    )
    # Optionally trigger an immediate run with the new schedule
    # Uncomment the line below if you want immediate execution on schedule update
    # return create_periodic_schedule(connector_id, workspace_id, user_id, connector_type, frequency_minutes)
    return True
