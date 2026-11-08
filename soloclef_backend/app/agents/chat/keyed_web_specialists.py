"""Which of the Exa, Tavily and Browserbase chat specialists a workspace has.

Each one runs on its provider's API key, which lives in one of the
workspace's MCP connectors (Workspace settings -> Connectors -> MCPs; the
same key the research box uses). A specialist whose key is missing is not
built for the turn, so the model is never offered a tool that cannot run:
a notebook pick of one becomes ``SourcesOnly.unavailable_picks`` (the
factory's ``sources_only.without(...)``), and the prompt says it is not
available. Whether a turn may use one at all is the retrieval scope's call
(``WEB_RETRIEVAL_SUBAGENTS``): a notebook chat has one only when it was
picked from the composer's Tools menu for that message.
"""

from __future__ import annotations

from collections.abc import Iterable

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import SearchSourceConnector, SearchSourceConnectorType
from app.notebooks.research.web_apis.keys import KeyOwner, key_from_connector

# Specialist (subagent) name -> whose key it runs on.
KEYED_WEB_SPECIALISTS: dict[str, KeyOwner] = {
    "exa_search": "exa",
    "tavily_search": "tavily",
    "browserbase": "browserbase",
}


async def connected_keyed_web_specialists(
    session: AsyncSession, workspace_id: int
) -> frozenset[str]:
    """The keyed specialists this workspace has a key for (never the keys)."""
    rows = (
        await session.execute(
            select(SearchSourceConnector.connector_type, SearchSourceConnector.config)
            .where(
                SearchSourceConnector.workspace_id == workspace_id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.MCP_CONNECTOR,
            )
            .order_by(SearchSourceConnector.id)
        )
    ).all()
    owners = {
        owner
        for connector_type, config in rows
        for owner in set(KEYED_WEB_SPECIALISTS.values())
        if key_from_connector(connector_type, config, owner)
    }
    return frozenset(
        name for name, owner in KEYED_WEB_SPECIALISTS.items() if owner in owners
    )


def excluded_keyed_web_specialists(connected: Iterable[str] | None) -> set[str]:
    """Keyed specialists to leave out of the turn: those without a key."""
    return set(KEYED_WEB_SPECIALISTS) - set(connected or ())


__all__ = [
    "KEYED_WEB_SPECIALISTS",
    "connected_keyed_web_specialists",
    "excluded_keyed_web_specialists",
]
