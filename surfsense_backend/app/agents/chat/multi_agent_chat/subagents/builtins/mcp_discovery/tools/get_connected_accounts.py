"""``get_connected_accounts`` — list the workspace's MCP servers.

Read-only helper the MCP subagent calls to discover which servers are
connected. Only ``display_name`` is exposed from the stored config — tokens,
secrets, and raw ``server_config`` are never returned.
"""

from __future__ import annotations

import json
import logging

from langchain_core.tools import BaseTool, StructuredTool
from sqlalchemy import select

from app.capabilities.core import ActivityDescriptor

logger = logging.getLogger(__name__)

_MCP_CONNECTOR_TYPE = "MCP_CONNECTOR"

_TOOL_DESCRIPTION = (
    "List the user's MCP servers with their names. Use this to find out which "
    "servers are connected and to pick the right one before acting when more "
    "than one could match. Read-only."
)


def _connector_type_value(connector_type: object) -> str:
    return (
        connector_type.value
        if hasattr(connector_type, "value")
        else str(connector_type)
    )


def create_get_connected_accounts_tool(*, workspace_id: int) -> BaseTool:
    """Factory for the read-only ``get_connected_accounts`` tool."""
    _workspace_id = workspace_id

    async def _impl() -> str:
        # Open a fresh session inside the closure: the factory-time session may
        # be closed by the time the LLM calls this tool.
        from app.db import SearchSourceConnector, async_session_maker

        accounts: list[dict[str, object]] = []
        try:
            async with async_session_maker() as session:
                result = await session.execute(
                    select(SearchSourceConnector).where(
                        SearchSourceConnector.workspace_id == _workspace_id,
                    )
                )
                connectors = list(result.scalars())
        except Exception:
            logger.exception("get_connected_accounts: connector query failed")
            return json.dumps({"accounts": [], "error": "query_failed"})

        for connector in connectors:
            ct = _connector_type_value(connector.connector_type)
            if ct != _MCP_CONNECTOR_TYPE:
                continue

            cfg = connector.config if isinstance(connector.config, dict) else {}
            account_meta: dict[str, object] = {}
            display_name = cfg.get("display_name")
            if display_name:
                account_meta["display_name"] = display_name

            accounts.append(
                {
                    "connector_id": connector.id,
                    "name": connector.name,
                    "connector_type": ct,
                    "app": "Custom MCP server",
                    # ``server_config`` presence == the connector produces agent
                    # tools.
                    "usable_in_chat": "server_config" in cfg,
                    "account": account_meta,
                }
            )

        return json.dumps({"accounts": accounts})

    return StructuredTool.from_function(
        name="get_connected_accounts",
        description=_TOOL_DESCRIPTION,
        coroutine=_impl,
        metadata={
            "activity_descriptor": ActivityDescriptor(
                active_title="Checking connected apps",
                completed_title="Checked connected apps",
                category="connector",
                icon_key="search",
                kind="get_connected_accounts",
                lifecycle="phase",
            ).as_metadata()
        },
    )
