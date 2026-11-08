"""A member's own MCP servers go when that person's membership ends.

A ``member`` server (``server_origin``) runs on the key, and the bill, of the
person who added it, and every member of the workspace uses it. When that
person leaves the workspace or is removed from it, nobody left could edit it
(only the adder or an administrator edits) and the Editor role cannot delete
it, so it would keep spending their key with no way for them to stop it.
Their member servers in that workspace are therefore deleted together with
the membership, in the same transaction. Site servers (the services the site
provides on its own keys, and every row from before the split) stay.
"""

from __future__ import annotations

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import SearchSourceConnector, SearchSourceConnectorType

from .server_origin import MEMBER, origin_of


async def delete_member_servers(
    session: AsyncSession, *, workspace_id: int, user_id: uuid.UUID
) -> list[int]:
    """Delete the member servers ``user_id`` added to this workspace.

    Returns their ids. The caller commits, then calls ``forget_servers`` so
    no chat keeps using the cached tools.
    """
    rows = (
        (
            await session.execute(
                select(SearchSourceConnector).where(
                    SearchSourceConnector.workspace_id == workspace_id,
                    SearchSourceConnector.user_id == user_id,
                    SearchSourceConnector.connector_type
                    == SearchSourceConnectorType.MCP_CONNECTOR,
                )
            )
        )
        .scalars()
        .all()
    )
    removed: list[int] = []
    for row in rows:
        if origin_of(row.config) == MEMBER:
            removed.append(row.id)
            await session.delete(row)
    return removed


def forget_servers(workspace_id: int) -> None:
    """Drop the workspace's cached MCP tools after servers were deleted."""
    # Imported here: the chat agent package imports the routes' dependencies.
    from app.agents.chat.multi_agent_chat.shared.tools.mcp.tool import (
        invalidate_mcp_tools_cache,
    )

    invalidate_mcp_tools_cache(workspace_id)
