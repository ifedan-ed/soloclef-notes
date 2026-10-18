"""Which account connections are switched on in a workspace.

A connection (``SearchSourceConnector``) is owned by one account
(``user_id``). It is enabled in **every** workspace that account belongs to,
automatically, unless the owner has explicitly switched it off there (a
``SearchSourceConnectorEnablement`` row with ``enabled=False``). Once
enabled in a shared workspace, every member of that workspace can use it
through the agent's tools — the owner decides on/off per workspace;
workspace membership decides who can use an enabled one.

``MCP_CONNECTOR`` is excluded everywhere here: it stays workspace-scoped
(one row per workspace, no account/enablement concept), unchanged by this
module. See ``custom/ACCOUNT_CONNECTIONS.md``.
"""

from __future__ import annotations

from sqlalchemy import exists, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import (
    SearchSourceConnector,
    SearchSourceConnectorEnablement,
    SearchSourceConnectorType,
    WorkspaceMembership,
)


def _not_disabled_in_workspace(workspace_id: int):
    """True unless the owner explicitly switched this connector off here."""
    return ~exists(
        select(SearchSourceConnectorEnablement.id).where(
            SearchSourceConnectorEnablement.connector_id == SearchSourceConnector.id,
            SearchSourceConnectorEnablement.workspace_id == workspace_id,
            SearchSourceConnectorEnablement.enabled.is_(False),
        )
    )


def _owner_is_member(workspace_id: int):
    return SearchSourceConnector.user_id.in_(
        select(WorkspaceMembership.user_id).where(
            WorkspaceMembership.workspace_id == workspace_id
        )
    )


async def enabled_connectors_in_workspace(
    session: AsyncSession,
    workspace_id: int,
    connector_type: SearchSourceConnectorType | None = None,
) -> list[SearchSourceConnector]:
    """Every non-MCP connection enabled in ``workspace_id``, any owner.

    "Enabled" = the connector's owner is a member of this workspace AND the
    owner hasn't explicitly disabled it here. Default is on; a row only
    exists to record an override (a disable, or workspace-specific
    indexing settings).
    """
    conditions = [
        SearchSourceConnector.connector_type
        != SearchSourceConnectorType.MCP_CONNECTOR,
        _owner_is_member(workspace_id),
        _not_disabled_in_workspace(workspace_id),
    ]
    if connector_type is not None:
        conditions.append(SearchSourceConnector.connector_type == connector_type)
    result = await session.execute(select(SearchSourceConnector).where(*conditions))
    return list(result.scalars().all())


async def is_enabled_in_workspace(
    session: AsyncSession, connector: SearchSourceConnector, workspace_id: int
) -> bool:
    """Same rule as :func:`enabled_connectors_in_workspace`, for one connector."""
    if connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR:
        return connector.workspace_id == workspace_id
    is_member = await session.scalar(
        select(WorkspaceMembership.id).where(
            WorkspaceMembership.workspace_id == workspace_id,
            WorkspaceMembership.user_id == connector.user_id,
        )
    )
    if is_member is None:
        return False
    disabled = await session.scalar(
        select(SearchSourceConnectorEnablement.id).where(
            SearchSourceConnectorEnablement.connector_id == connector.id,
            SearchSourceConnectorEnablement.workspace_id == workspace_id,
            SearchSourceConnectorEnablement.enabled.is_(False),
        )
    )
    return disabled is None


async def get_enablement_row(
    session: AsyncSession, connector_id: int, workspace_id: int
) -> SearchSourceConnectorEnablement | None:
    return await session.scalar(
        select(SearchSourceConnectorEnablement).where(
            SearchSourceConnectorEnablement.connector_id == connector_id,
            SearchSourceConnectorEnablement.workspace_id == workspace_id,
        )
    )


async def clear_disable_if_present(
    session: AsyncSession, connector_id: int, workspace_id: int
) -> None:
    """Undo a prior explicit disable, if any. Leaves no row when there wasn't one.

    Used after (re)connecting from a workspace: the connection is already on
    by default, so this only matters when it had been switched off here
    before — the common "just connected" case is a no-op, not a new row.
    """
    row = await get_enablement_row(session, connector_id, workspace_id)
    if row is not None and not row.enabled:
        row.enabled = True
        await session.flush()


async def set_enabled_in_workspace(
    session: AsyncSession,
    *,
    connector: SearchSourceConnector,
    workspace_id: int,
    enabled: bool,
    enabled_by_user_id,
) -> SearchSourceConnectorEnablement:
    """Explicit on/off override. Only the connector's owner may call this."""
    row = await get_enablement_row(session, connector.id, workspace_id)
    if row is None:
        row = SearchSourceConnectorEnablement(
            connector_id=connector.id,
            workspace_id=workspace_id,
            enabled_by_user_id=enabled_by_user_id,
        )
        session.add(row)
    row.enabled = enabled
    if not enabled:
        # Switching off also stops any periodic indexing configured here;
        # re-enabling starts from a clean slate, never a stale schedule.
        row.periodic_indexing_enabled = False
        row.next_scheduled_at = None
    await session.flush()
    return row
