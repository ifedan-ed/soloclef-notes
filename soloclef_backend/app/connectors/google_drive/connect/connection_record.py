"""A person's Google Drive connection: their account's oldest Drive connector row.

Account-level, not per workspace (``custom/ACCOUNT_CONNECTIONS.md``): one
person connecting Drive from workspace A and workspace B reuses the same
row and its credentials, and it stays enabled in both unless switched off.
``workspace_id`` is kept on every function here only to call
``connector_enablement.set_enabled_in_workspace`` after create/reuse, so the
workspace the person connected from has it switched on right away.
"""

from __future__ import annotations

from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import SearchSourceConnector, SearchSourceConnectorType
from app.services.connector_enablement import clear_disable_if_present


async def users_connection(
    session: AsyncSession, user_id: UUID, workspace_id: int
) -> SearchSourceConnector | None:
    del workspace_id  # account-wide lookup now; kept for call-site stability
    return await session.scalar(
        select(SearchSourceConnector)
        .where(
            SearchSourceConnector.user_id == user_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        )
        .order_by(SearchSourceConnector.id)
        .limit(1)
    )


async def ensure_enabled(
    session: AsyncSession, connector: SearchSourceConnector, workspace_id: int
) -> None:
    """Make sure the workspace the person connected/reconnected from has it on.

    A no-op if it's already on (the common case, since enabling is the
    default) — only clears a prior explicit disable for this workspace.
    """
    await clear_disable_if_present(session, connector.id, workspace_id)


async def owned_drive_connector(
    session: AsyncSession, user_id: UUID, connector_id: int
) -> SearchSourceConnector | None:
    """The connector, only if it is a Drive connection of this person."""
    return await session.scalar(
        select(SearchSourceConnector).where(
            SearchSourceConnector.id == connector_id,
            SearchSourceConnector.user_id == user_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        )
    )
