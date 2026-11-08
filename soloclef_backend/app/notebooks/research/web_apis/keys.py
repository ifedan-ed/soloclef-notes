"""Find a workspace's Exa, Tavily or Browserbase API key among its connectors.

All three are usually connected as MCP servers (Settings -> Connectors), whose
config carries the key the MCP endpoint authenticates with: Exa's in an
``x-api-key`` header (or ``exaApiKey`` in the URL), Tavily's and
Browserbase's as a bearer token (or ``tavilyApiKey`` /
``browserbaseApiKey`` in the URL). The same key works on the providers' REST
APIs. Only Exa and Tavily are search providers (``PROVIDERS``); Browserbase
reads one page (``browserbase_key``).
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Literal
from urllib.parse import parse_qs, urlsplit

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import SearchSourceConnector, SearchSourceConnectorType

Provider = Literal["exa", "tavily"]
PROVIDERS: tuple[Provider, ...] = ("exa", "tavily")
KeyOwner = Literal["exa", "tavily", "browserbase"]

_HOSTS: dict[KeyOwner, str] = {
    "exa": "exa.ai",
    "tavily": "tavily.com",
    "browserbase": "browserbase.com",
}
_URL_PARAMS: dict[KeyOwner, tuple[str, ...]] = {
    "exa": ("exaApiKey", "exa_api_key", "apiKey", "api_key"),
    "tavily": ("tavilyApiKey", "tavily_api_key", "apiKey", "api_key"),
    "browserbase": ("browserbaseApiKey", "browserbase_api_key", "apiKey", "api_key"),
}


def _host_matches(url: str, provider: KeyOwner) -> bool:
    host = (urlsplit(url).hostname or "").lower()
    domain = _HOSTS[provider]
    return host == domain or host.endswith("." + domain)


def _header(headers: Mapping[str, Any], name: str) -> str | None:
    for key, value in headers.items():
        if isinstance(key, str) and key.lower() == name and isinstance(value, str):
            return value.strip() or None
    return None


def key_from_mcp_config(config: Mapping[str, Any], provider: KeyOwner) -> str | None:
    """The API key in an MCP connector's config, if it points at ``provider``."""
    server = config.get("server_config")
    if not isinstance(server, Mapping):
        return None
    url = server.get("url")
    if not isinstance(url, str) or not _host_matches(url, provider):
        return None
    headers = server.get("headers")
    headers = headers if isinstance(headers, Mapping) else {}
    own_header = {"exa": "x-api-key", "browserbase": "x-bb-api-key"}.get(provider)
    if own_header:
        key = _header(headers, own_header)
        if key:
            return key
    bearer = _header(headers, "authorization")
    if bearer and bearer.lower().startswith("bearer "):
        key = bearer[7:].strip()
        if key:
            return key
    query = parse_qs(urlsplit(url).query)
    for name in _URL_PARAMS[provider]:
        values = query.get(name)
        if values and values[0].strip():
            return values[0].strip()
    return None


def key_from_connector(
    connector_type: str, config: Mapping[str, Any] | None, provider: KeyOwner
) -> str | None:
    if not isinstance(config, Mapping):
        return None
    if connector_type == SearchSourceConnectorType.MCP_CONNECTOR:
        return key_from_mcp_config(config, provider)
    return None


async def workspace_keys(
    session: AsyncSession, workspace_id: int
) -> dict[Provider, str]:
    """Each provider's key in this workspace; a provider with none is left out.

    The oldest matching connector wins, so the choice is stable.
    """
    result = await session.execute(
        select(SearchSourceConnector.connector_type, SearchSourceConnector.config)
        .where(
            SearchSourceConnector.workspace_id == workspace_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.MCP_CONNECTOR,
        )
        .order_by(SearchSourceConnector.id)
    )
    keys: dict[Provider, str] = {}
    for connector_type, config in result.all():
        for provider in PROVIDERS:
            if provider in keys:
                continue
            key = key_from_connector(connector_type, config, provider)
            if key:
                keys[provider] = key
    return keys


async def browserbase_key(session: AsyncSession, workspace_id: int) -> str | None:
    """The workspace's Browserbase API key (oldest connector wins), or None.

    Read server-side only; it is never sent to the browser.
    """
    result = await session.execute(
        select(SearchSourceConnector.connector_type, SearchSourceConnector.config)
        .where(
            SearchSourceConnector.workspace_id == workspace_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.MCP_CONNECTOR,
        )
        .order_by(SearchSourceConnector.id)
    )
    for connector_type, config in result.all():
        key = key_from_connector(connector_type, config, "browserbase")
        if key:
            return key
    return None
