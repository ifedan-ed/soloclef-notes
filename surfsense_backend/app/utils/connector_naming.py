"""
Connector Naming Utilities.

Provides functions for generating unique, user-friendly connector names.

Uniqueness scope matches the model's two owners: ``MCP_CONNECTOR`` stays
workspace-scoped (``workspace_id`` is still the row's real workspace), every
other connector type is account-scoped (``workspace_id`` is NULL on the row,
so the filter below is ``IS NULL``, not a value match) — see
``custom/ACCOUNT_CONNECTIONS.md``.
"""

from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.sql import func

from app.db import SearchSourceConnector, SearchSourceConnectorType

# Friendly display names for connector types
BASE_NAME_FOR_TYPE = {
    SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR: "Google Drive",
    SearchSourceConnectorType.NEXTCLOUD_CONNECTOR: "Nextcloud files",
    SearchSourceConnectorType.MCP_CONNECTOR: "Model Context Protocol (MCP)",
}


def get_base_name_for_type(connector_type: SearchSourceConnectorType) -> str:
    """Get a friendly display name for a connector type."""
    return BASE_NAME_FOR_TYPE.get(
        connector_type, connector_type.value.replace("_", " ").title()
    )


def generate_connector_name_with_identifier(
    connector_type: SearchSourceConnectorType,
    identifier: str | None,
) -> str:
    """
    Generate a connector name with an identifier.

    Args:
        connector_type: The type of connector
        identifier: User identifier (email, workspace name, etc.)

    Returns:
        Name like "Google Drive - john@example.com" or just "Google Drive" if no identifier
    """
    base = get_base_name_for_type(connector_type)
    if identifier:
        return f"{base} - {identifier}"
    return base


def _scope_clause(connector_type: SearchSourceConnectorType, workspace_id: int):
    """MCP stays workspace-scoped; everything else is account-scoped (NULL row)."""
    if connector_type == SearchSourceConnectorType.MCP_CONNECTOR:
        return SearchSourceConnector.workspace_id == workspace_id
    return SearchSourceConnector.workspace_id.is_(None)


async def count_connectors_of_type(
    session: AsyncSession,
    connector_type: SearchSourceConnectorType,
    workspace_id: int,
    user_id: UUID,
) -> int:
    """Count existing connectors of a type for a user (in this workspace, for MCP;
    account-wide for every other type)."""
    result = await session.execute(
        select(func.count(SearchSourceConnector.id)).where(
            SearchSourceConnector.connector_type == connector_type,
            _scope_clause(connector_type, workspace_id),
            SearchSourceConnector.user_id == user_id,
        )
    )
    return result.scalar() or 0


async def check_duplicate_connector(
    session: AsyncSession,
    connector_type: SearchSourceConnectorType,
    workspace_id: int,
    user_id: UUID,
    identifier: str | None,
) -> bool:
    """
    Check if a connector with the same identifier already exists.

    Args:
        session: Database session
        connector_type: The type of connector
        workspace_id: The workspace ID (only meaningful for MCP; see module docstring)
        user_id: The user ID
        identifier: User identifier (email, workspace name, etc.)

    Returns:
        True if a duplicate exists, False otherwise
    """
    if not identifier:
        return False

    expected_name = f"{get_base_name_for_type(connector_type)} - {identifier}"
    result = await session.execute(
        select(func.count(SearchSourceConnector.id)).where(
            SearchSourceConnector.connector_type == connector_type,
            _scope_clause(connector_type, workspace_id),
            SearchSourceConnector.user_id == user_id,
            SearchSourceConnector.name == expected_name,
        )
    )
    return (result.scalar() or 0) > 0


async def ensure_unique_connector_name(
    session: AsyncSession,
    name: str,
    workspace_id: int,
    user_id: UUID,
    connector_type: SearchSourceConnectorType = SearchSourceConnectorType.MCP_CONNECTOR,
) -> str:
    """
    Ensure a connector name is unique within its scope (the user's account,
    or for MCP, the user's row in this workspace).

    If the name already exists, appends a counter suffix: (2), (3), etc.
    Uses the same suffix format as generate_unique_connector_name.

    Args:
        session: Database session
        name: Desired connector name
        workspace_id: The workspace ID (only meaningful for MCP)
        user_id: The user ID
        connector_type: Defaults to MCP for backward compatibility with the
            one existing caller that didn't pass it; every new caller should.

    Returns:
        Unique name, either the original or with a counter suffix
    """
    result = await session.execute(
        select(SearchSourceConnector.name).where(
            _scope_clause(connector_type, workspace_id),
            SearchSourceConnector.user_id == user_id,
        )
    )
    existing_names = {row[0] for row in result.all()}

    if name not in existing_names:
        return name

    counter = 2
    while f"{name} ({counter})" in existing_names:
        counter += 1
    return f"{name} ({counter})"


async def generate_unique_connector_name(
    session: AsyncSession,
    connector_type: SearchSourceConnectorType,
    workspace_id: int,
    user_id: UUID,
    identifier: str | None = None,
) -> str:
    """
    Generate a unique connector name.

    If an identifier is provided (email, workspace name, etc.), uses it with base name.
    Otherwise, falls back to counting existing connectors for uniqueness.

    Args:
        session: Database session
        connector_type: The type of connector
        workspace_id: The workspace ID (only meaningful for MCP; see module docstring)
        user_id: The user ID
        identifier: Optional user identifier (email, workspace name, etc.)

    Returns:
        Unique name like "Google Drive - john@example.com" or "Google Drive (2)"
    """
    base = get_base_name_for_type(connector_type)

    if identifier:
        name = f"{base} - {identifier}"
        return await ensure_unique_connector_name(
            session,
            name,
            workspace_id,
            user_id,
            connector_type,
        )

    count = await count_connectors_of_type(
        session, connector_type, workspace_id, user_id
    )

    if count == 0:
        return base
    return f"{base} ({count + 1})"
