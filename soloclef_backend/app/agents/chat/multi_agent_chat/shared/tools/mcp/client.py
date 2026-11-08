"""MCP connection test for remote (HTTP) servers.

Local stdio servers are not supported: they ran a command inside the backend
container with its whole environment. Every connection goes through
``app.mcp_security`` (the hosts the caller passes: the allowlist for a site
server, the server's own host for a member's; public-address check pinned into
the connect, no redirects).
"""

import logging
from typing import Any

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

from app.mcp_security.url_guard import (
    MCPDestinationRefusedError,
    check_destination,
    guarded_http_client_factory,
)

logger = logging.getLogger(__name__)

GENERIC_FAILURE = "Could not connect to the MCP server. Check the URL and key."


async def test_mcp_http_connection(
    url: str,
    headers: dict[str, str] | None,
    allowed_hosts: frozenset[str],
) -> dict[str, Any]:
    """Connect, initialize and list tools; the reply never carries error text.

    A refused destination and a failed connection return the same generic
    message, so the endpoint cannot be used to map what is reachable.
    """
    try:
        await check_destination(url, allowed_hosts)
        async with (
            streamablehttp_client(
                url,
                headers=headers or {},
                httpx_client_factory=guarded_http_client_factory(allowed_hosts),
            ) as (read, write, _),
            ClientSession(read, write) as session,
        ):
            await session.initialize()
            response = await session.list_tools()
            tools = [
                {
                    "name": tool.name,
                    "description": tool.description or "",
                    "input_schema": tool.inputSchema
                    if hasattr(tool, "inputSchema")
                    else {},
                }
                for tool in response.tools
            ]
    except MCPDestinationRefusedError as exc:
        logger.info("MCP test refused: %s", exc)
        return {"status": "error", "message": GENERIC_FAILURE, "tools": []}
    except Exception as exc:
        logger.info("MCP test connection failed: %s", type(exc).__name__)
        return {"status": "error", "message": GENERIC_FAILURE, "tools": []}

    return {
        "status": "success",
        "message": f"Connected successfully. Found {len(tools)} tools.",
        "tools": tools,
    }
