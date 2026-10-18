"""A person's Nextcloud connection: one connector row per account, theirs alone.

Account-level, not per workspace (``custom/ACCOUNT_CONNECTIONS.md``): one
Nextcloud login is reused by every workspace that account belongs to, and
stays enabled in all of them unless switched off. Connecting again replaces
the credentials in the same row, so imported files stay linked to it
(refresh finds them by account, not by row).
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import SearchSourceConnector, SearchSourceConnectorType
from app.services.connector_enablement import clear_disable_if_present

DEFAULT_NAME = "Nextcloud"


async def users_connection(
    session: AsyncSession, user_id: UUID, workspace_id: int
) -> SearchSourceConnector | None:
    del workspace_id  # account-wide lookup now; kept for call-site stability
    return await session.scalar(
        select(SearchSourceConnector)
        .where(
            SearchSourceConnector.user_id == user_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR,
        )
        .order_by(SearchSourceConnector.id)
        .limit(1)
    )


async def save_connection(
    session: AsyncSession,
    *,
    user_id: UUID,
    workspace_id: int,
    config: dict[str, Any],
) -> tuple[SearchSourceConnector, dict[str, Any] | None]:
    """The saved row and the config it replaced (None for a new connection)."""
    connector = await users_connection(session, user_id, workspace_id)
    if connector is not None:
        previous = dict(connector.config or {})
        connector.config = config
        # Reconnecting never resets whichever other workspaces already have
        # it on; only clears an explicit disable for *this* workspace.
        await clear_disable_if_present(session, connector.id, workspace_id)
        await session.flush()
        return connector, previous
    connector = SearchSourceConnector(
        name=DEFAULT_NAME,
        connector_type=SearchSourceConnectorType.NEXTCLOUD_CONNECTOR,
        is_indexable=False,
        periodic_indexing_enabled=False,
        indexing_frequency_minutes=None,
        config=config,
        workspace_id=None,
        user_id=user_id,
    )
    session.add(connector)
    await session.flush()
    return connector, None
