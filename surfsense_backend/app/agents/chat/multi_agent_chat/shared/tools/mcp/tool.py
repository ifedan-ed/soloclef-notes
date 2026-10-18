"""MCP Tool Factory.

This module creates LangChain tools from MCP servers using the Model Context Protocol.
Tools are dynamically discovered from MCP servers - no manual configuration needed.

Only remote servers (streamable-http/http/sse: url, headers) are loaded. A site
server (one an administrator added) must be on a host from the admin allowlist;
a workspace member's own server may be any public https host
(``app.mcp_security.server_origin``). Both are checked again at every
connection. Local stdio servers are refused with a warning: they ran
a command inside the backend container.

All MCP tools are unconditionally gated by HITL (Human-in-the-Loop) approval.
Per the MCP spec: "Clients MUST consider tool annotations to be untrusted unless
they come from trusted servers."  Users can bypass HITL for specific tools by
clicking "Always Allow", which adds the tool name to the connector's
``config.trusted_tools`` allow-list.
"""

from __future__ import annotations

import asyncio
import logging
import time
from typing import Any

from langchain_core.tools import StructuredTool
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client
from pydantic import BaseModel, ConfigDict, Field, create_model
from sqlalchemy import cast, select
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.ext.asyncio import AsyncSession

from app.agents.chat.multi_agent_chat.shared.middleware.dedup_tool_calls import (
    dedup_key_full_args,
)
from app.agents.chat.multi_agent_chat.shared.tools.hitl import request_approval
from app.agents.chat.multi_agent_chat.shared.tools.mcp.cache import (
    CachedMCPTools,
    read_cached_tools,
    write_cached_tools,
)
from app.db import SearchSourceConnector, SearchSourceConnectorType
from app.mcp_security.server_origin import (
    SITE,
    Origin,
    origin_of,
    reachable_hosts,
)
from app.mcp_security.tool_trust import effective_trusted
from app.mcp_security.url_guard import (
    HTTP_TRANSPORTS,
    MCPDestinationRefusedError,
    check_destination,
    check_url_shape,
    guarded_http_client_factory,
)
from app.utils.perf import get_perf_logger

_perf_log = get_perf_logger()

logger = logging.getLogger(__name__)

_MCP_CACHE_TTL_SECONDS = 300  # 5 minutes
_MCP_CACHE_MAX_SIZE = 50
_MCP_DISCOVERY_TIMEOUT_SECONDS = 30
_TOOL_CALL_MAX_RETRIES = 3
_TOOL_CALL_RETRY_DELAY = 1.5  # seconds, doubles per attempt
# Keyed by ``(workspace_id, bypass_internal_hitl)`` so single-agent and
# multi-agent paths cannot share tool closures with different HITL wiring.
_MCPCacheKey = tuple[int, bool]
_mcp_tools_cache: dict[_MCPCacheKey, tuple[float, list[StructuredTool]]] = {}


def _evict_expired_mcp_cache() -> None:
    """Remove expired entries from the MCP tools cache to prevent unbounded growth."""
    now = time.monotonic()
    expired = [
        k
        for k, (ts, _) in _mcp_tools_cache.items()
        if now - ts >= _MCP_CACHE_TTL_SECONDS
    ]
    for k in expired:
        del _mcp_tools_cache[k]
    if expired:
        logger.debug("Evicted %d expired MCP cache entries", len(expired))


def _create_dynamic_input_model_from_schema(
    tool_name: str,
    input_schema: dict[str, Any],
) -> type[BaseModel]:
    """Create a Pydantic model from MCP tool's JSON schema.

    Models always allow extra fields (``extra="allow"``) so that parameters
    missing from a broken or incomplete JSON schema (e.g. ``zod-to-json-schema``
    producing an empty ``$schema``-only object) can still be forwarded to the
    MCP server.

    When the schema declares **no** properties, a synthetic ``input_data``
    field of type ``dict`` is injected so the LLM has a visible parameter to
    populate.  The caller should unpack ``input_data`` before forwarding to
    the MCP server (see ``_unpack_synthetic_input_data``).
    """
    properties = input_schema.get("properties", {})
    required_fields = input_schema.get("required", [])

    field_definitions = {}
    for param_name, param_schema in properties.items():
        param_description = param_schema.get("description", "")
        is_required = param_name in required_fields

        if is_required:
            field_definitions[param_name] = (
                Any,
                Field(..., description=param_description),
            )
        else:
            field_definitions[param_name] = (
                Any | None,
                Field(None, description=param_description),
            )

    if not properties:
        field_definitions["input_data"] = (
            dict[str, Any] | None,
            Field(
                None,
                description=(
                    "Arguments to pass to this tool as a JSON object. "
                    "Infer sensible key names from the tool name and description "
                    '(e.g. {"search": "my query"} for a search tool).'
                ),
            ),
        )

    model_name = f"{tool_name.replace(' ', '').replace('-', '_')}Input"
    model = create_model(
        model_name, __config__=ConfigDict(extra="allow"), **field_definitions
    )
    return model


def _unpack_synthetic_input_data(kwargs: dict[str, Any]) -> dict[str, Any]:
    """Unpack the synthetic ``input_data`` field into top-level kwargs.

    When the MCP tool schema is empty, ``_create_dynamic_input_model_from_schema``
    adds a catch-all ``input_data: dict`` field.  This helper merges that dict
    back into the top-level kwargs so the MCP server receives flat arguments.
    """
    input_data = kwargs.pop("input_data", None)
    if isinstance(input_data, dict):
        kwargs.update(input_data)
    return kwargs


async def _create_mcp_tool_from_definition_http(
    tool_def: dict[str, Any],
    url: str,
    headers: dict[str, str],
    *,
    connector_name: str = "",
    connector_id: int | None = None,
    trusted_tools: list[str] | None = None,
    bypass_internal_hitl: bool = False,
    origin: Origin = SITE,
) -> StructuredTool:
    """Create a LangChain tool from an MCP tool definition (HTTP transport).

    Every tool is wrapped with HITL approval. Set ``bypass_internal_hitl=True``
    when an outer ``HumanInTheLoopMiddleware`` already gates the tool.
    """
    original_tool_name = tool_def.get("name", "unnamed_tool")
    raw_description = tool_def.get("description", "No description provided")
    input_schema = tool_def.get("input_schema", {"type": "object", "properties": {}})

    exposed_name = original_tool_name
    if connector_name:
        tool_description = f"[MCP server: {connector_name}] {raw_description}"
    else:
        tool_description = raw_description

    logger.debug("MCP HTTP tool '%s' input schema: %s", exposed_name, input_schema)

    input_model = _create_dynamic_input_model_from_schema(exposed_name, input_schema)

    async def _do_mcp_call(
        call_headers: dict[str, str],
        call_kwargs: dict[str, Any],
        timeout: float = 60.0,
    ) -> str:
        """Execute a single MCP HTTP call with the given headers."""
        call_start = time.perf_counter()
        allowed_hosts = await reachable_hosts(origin, url)
        check_url_shape(url, allowed_hosts)
        async with (
            streamablehttp_client(
                url,
                headers=call_headers,
                httpx_client_factory=guarded_http_client_factory(allowed_hosts),
            ) as (read, write, _),
            ClientSession(read, write) as session,
        ):
            init_start = time.perf_counter()
            await session.initialize()
            init_elapsed = time.perf_counter() - init_start

            tool_start = time.perf_counter()
            response = await asyncio.wait_for(
                session.call_tool(original_tool_name, arguments=call_kwargs),
                timeout=timeout,
            )
            tool_elapsed = time.perf_counter() - tool_start

            result = []
            for content in response.content:
                if hasattr(content, "text"):
                    result.append(content.text)
                elif hasattr(content, "data"):
                    result.append(str(content.data))
                else:
                    result.append(str(content))

            payload = "\n".join(result) if result else ""

        _perf_log.info(
            "[mcp_http_call] connector=%s tool=%s init=%.3fs call=%.3fs total=%.3fs out_chars=%d",
            connector_id,
            original_tool_name,
            init_elapsed,
            tool_elapsed,
            time.perf_counter() - call_start,
            len(payload),
        )
        return payload

    async def mcp_http_tool_call(**kwargs) -> str:
        """Execute the MCP tool call via HTTP transport."""
        logger.debug("MCP HTTP tool '%s' called", exposed_name)

        if bypass_internal_hitl:
            call_kwargs = _unpack_synthetic_input_data(
                {k: v for k, v in kwargs.items() if v is not None}
            )
        else:
            hitl_result = request_approval(
                action_type="mcp_tool_call",
                tool_name=exposed_name,
                params=kwargs,
                context={
                    "mcp_server": connector_name,
                    "tool_description": raw_description,
                    "mcp_transport": "http",
                    "mcp_connector_id": connector_id,
                },
                trusted_tools=trusted_tools,
            )
            if hitl_result.rejected:
                return "Tool call rejected by user."
            call_kwargs = _unpack_synthetic_input_data(
                {k: v for k, v in hitl_result.params.items() if v is not None}
            )

        try:
            result_str = await _do_mcp_call(headers, call_kwargs)
        except MCPDestinationRefusedError:
            logger.warning(
                "MCP HTTP tool '%s' refused: connector %s is not on an allowed host",
                exposed_name,
                connector_id,
            )
            return f"Error: MCP tool '{exposed_name}' is not available."
        except Exception as first_err:
            if not _is_auth_error(first_err) or connector_id is None:
                logger.exception(
                    "MCP HTTP tool '%s' execution failed: %s", exposed_name, first_err
                )
                return f"Error: MCP HTTP tool '{exposed_name}' execution failed: {first_err!s}"

            logger.warning(
                "MCP HTTP tool '%s' got 401 for connector %s",
                exposed_name,
                connector_id,
            )
            await _mark_connector_auth_expired(connector_id)
            return (
                f"Error: MCP tool '{exposed_name}' authentication expired. "
                "Please re-authenticate the connector in your settings."
            )
        logger.debug(
            "MCP HTTP tool '%s' succeeded (len=%d)", exposed_name, len(result_str)
        )
        return result_str

    tool = StructuredTool(
        name=exposed_name,
        description=tool_description,
        coroutine=mcp_http_tool_call,
        args_schema=input_model,
        metadata={
            "mcp_input_schema": input_schema,
            "mcp_transport": "http",
            "mcp_url": url,
            "mcp_connector_name": connector_name or None,
            "mcp_is_generic": True,
            "hitl": True,
            # Full-args hash: shared identifiers (cloudId, workspaceId, …)
            # would otherwise collapse legitimate batches.
            "dedup_key": dedup_key_full_args,
            "mcp_original_tool_name": original_tool_name,
            "mcp_connector_id": connector_id,
        },
    )

    logger.debug("Created MCP tool (HTTP): '%s'", exposed_name)
    return tool


def _refuse_non_http(connector_id: int, connector_name: str, transport: Any) -> None:
    """Local (stdio) and unknown transports are never started."""
    logger.warning(
        "MCP connector %d (name: '%s') uses transport %r, which is not supported; "
        "only remote https servers are loaded. Skipping it.",
        connector_id,
        connector_name,
        transport,
    )


async def _load_http_mcp_tools(
    connector_id: int,
    connector_name: str,
    server_config: dict[str, Any],
    trusted_tools: list[str] | None = None,
    *,
    bypass_internal_hitl: bool = False,
    cached_tools: CachedMCPTools | None = None,
    origin: Origin = SITE,
) -> list[StructuredTool]:
    """Load every tool an HTTP-based MCP server advertises.

    Args:
        cached_tools: If provided, skip live discovery and rebuild wrappers
            from the persisted definitions.
    """
    tools: list[StructuredTool] = []

    url = server_config.get("url")
    if not url or not isinstance(url, str):
        logger.warning(
            "MCP connector %d (name: '%s') missing or invalid url field, skipping",
            connector_id,
            connector_name,
        )
        return tools

    headers = server_config.get("headers", {})
    if not isinstance(headers, dict):
        logger.warning(
            "MCP connector %d (name: '%s') has invalid headers field (must be dict), skipping",
            connector_id,
            connector_name,
        )
        return tools

    allowed_hosts = await reachable_hosts(origin, url)
    try:
        if cached_tools is None:
            await check_destination(url, allowed_hosts)
        else:
            check_url_shape(url, allowed_hosts)
    except MCPDestinationRefusedError as exc:
        logger.warning(
            "MCP connector %d (name: '%s') refused: %s. Skipping it.",
            connector_id,
            connector_name,
            exc,
        )
        return tools

    async def _discover(
        disc_headers: dict[str, str],
    ) -> tuple[dict[str, str | None], list[dict[str, Any]]]:
        """Connect, initialize, and list tools — returns (serverInfo, tools)."""
        async with (
            streamablehttp_client(
                url,
                headers=disc_headers,
                httpx_client_factory=guarded_http_client_factory(allowed_hosts),
            ) as (read, write, _),
            ClientSession(read, write) as session,
        ):
            init_result = await session.initialize()
            server_info: dict[str, str | None] = {"name": None, "version": None}
            si = getattr(init_result, "serverInfo", None)
            if si is not None:
                server_info["name"] = getattr(si, "name", None)
                server_info["version"] = getattr(si, "version", None)

            response = await session.list_tools()
            return server_info, [
                {
                    "name": tool.name,
                    "description": tool.description or "",
                    "input_schema": tool.inputSchema
                    if hasattr(tool, "inputSchema")
                    else {},
                }
                for tool in response.tools
            ]

    if cached_tools is not None:
        tool_definitions = [
            {
                "name": td.name,
                "description": td.description,
                "input_schema": td.input_schema,
            }
            for td in cached_tools.tools
        ]
    else:
        try:
            server_info, tool_definitions = await _discover(headers)
        except Exception as first_err:
            if not _is_auth_error(first_err) or connector_id is None:
                logger.exception(
                    "Failed to connect to HTTP MCP server at '%s' (connector %d): %s",
                    url,
                    connector_id,
                    first_err,
                )
                return tools

            await _mark_connector_auth_expired(connector_id)
            logger.error(
                "HTTP MCP discovery for connector %d got 401, marking auth_expired",
                connector_id,
            )
            return tools

        await write_cached_tools(
            connector_id,
            tool_definitions,
            server_name=server_info.get("name"),
            server_version=server_info.get("version"),
            transport=server_config.get("transport", "streamable-http"),
        )

    total_discovered = len(tool_definitions)

    logger.info(
        "Discovered %d tools from HTTP MCP server '%s' (connector %d)",
        total_discovered,
        url,
        connector_id,
    )

    for tool_def in tool_definitions:
        try:
            tool = await _create_mcp_tool_from_definition_http(
                tool_def,
                url,
                headers,
                connector_name=connector_name,
                connector_id=connector_id,
                trusted_tools=trusted_tools,
                bypass_internal_hitl=bypass_internal_hitl,
                origin=origin,
            )
            tools.append(tool)
        except Exception as e:
            logger.exception(
                "Failed to create HTTP tool '%s' from connector %d: %s",
                tool_def.get("name"),
                connector_id,
                e,
            )

    return tools


# ---------------------------------------------------------------------------
# Reactive 401 handling helpers
# ---------------------------------------------------------------------------


def _is_auth_error(exc: Exception) -> bool:
    """Check if an exception indicates an HTTP 401 authentication failure."""
    try:
        import httpx

        if isinstance(exc, httpx.HTTPStatusError):
            return exc.response.status_code == 401
    except ImportError:
        pass
    err_str = str(exc).lower()
    return "401" in err_str or "unauthorized" in err_str


async def _mark_connector_auth_expired(connector_id: int) -> None:
    """Set ``config.auth_expired = True`` so the frontend shows re-auth UI."""
    from app.db import async_session_maker

    try:
        async with async_session_maker() as session:
            result = await session.execute(
                select(SearchSourceConnector).filter(
                    SearchSourceConnector.id == connector_id,
                )
            )
            connector = result.scalars().first()
            if not connector:
                return

            cfg = dict(connector.config or {})
            if cfg.get("auth_expired"):
                return

            cfg["auth_expired"] = True
            connector.config = cfg

            from sqlalchemy.orm.attributes import flag_modified

            flag_modified(connector, "config")
            await session.commit()

            logger.info(
                "Marked MCP connector %s as auth_expired after unrecoverable 401",
                connector_id,
            )
            invalidate_mcp_tools_cache(connector.workspace_id)

    except Exception:
        logger.warning(
            "Failed to mark connector %s as auth_expired",
            connector_id,
            exc_info=True,
        )


def invalidate_mcp_tools_cache(workspace_id: int | None = None) -> None:
    """Invalidate cached MCP tools (both ``bypass_internal_hitl`` variants together)."""
    if workspace_id is not None:
        for key in [k for k in _mcp_tools_cache if k[0] == workspace_id]:
            _mcp_tools_cache.pop(key, None)
    else:
        _mcp_tools_cache.clear()


async def discover_single_mcp_connector(connector_id: int) -> None:
    """Force live MCP discovery for one connector so its ``cached_tools`` row is fresh.

    ``_load_http_mcp_tools`` persists ``cached_tools`` as a side effect of any
    live discovery; passing ``cached_tools=None`` here guarantees we go to the
    network. The returned wrappers are discarded — the in-process LRU is
    rebuilt lazily on the next user query. Stdio connectors are not cached and
    are skipped.
    """
    from app.db import async_session_maker

    started = time.perf_counter()
    try:
        async with async_session_maker() as session:
            connector = await session.get(SearchSourceConnector, connector_id)
            if connector is None:
                logger.info(
                    "discover_single_mcp_connector: connector %d not found",
                    connector_id,
                )
                return

            cfg = connector.config or {}
            server_config = cfg.get("server_config", {})
            if not server_config or not isinstance(server_config, dict):
                return

            transport = server_config.get("transport", "stdio")
            if (
                connector.connector_type != SearchSourceConnectorType.MCP_CONNECTOR
                or transport not in HTTP_TRANSPORTS
            ):
                _refuse_non_http(connector.id, connector.name, transport)
                return

            await asyncio.wait_for(
                _load_http_mcp_tools(
                    connector.id,
                    connector.name,
                    server_config,
                    trusted_tools=effective_trusted(cfg.get("trusted_tools", [])),
                    bypass_internal_hitl=True,
                    cached_tools=None,
                    origin=origin_of(cfg),
                ),
                timeout=_MCP_DISCOVERY_TIMEOUT_SECONDS,
            )

            _perf_log.info(
                "[mcp_prefetch] connector=%s elapsed=%.3fs",
                connector_id,
                time.perf_counter() - started,
            )
    except TimeoutError:
        logger.warning(
            "discover_single_mcp_connector: connector %d timed out after %ds",
            connector_id,
            _MCP_DISCOVERY_TIMEOUT_SECONDS,
        )
    except Exception:
        logger.warning(
            "discover_single_mcp_connector: failed for connector %d",
            connector_id,
            exc_info=True,
        )


async def load_mcp_tools(
    session: AsyncSession,
    workspace_id: int,
    *,
    bypass_internal_hitl: bool = False,
) -> list[StructuredTool]:
    """Load all MCP tools from the user's active MCP server connectors.

    Results are cached per ``(workspace_id, bypass_internal_hitl)`` for up
    to 5 minutes; bypass is keyed because each variant builds a different tool
    closure (with vs. without the in-wrapper ``request_approval`` gate).
    """
    _evict_expired_mcp_cache()

    now = time.monotonic()
    cache_key: _MCPCacheKey = (workspace_id, bypass_internal_hitl)
    cached = _mcp_tools_cache.get(cache_key)
    if cached is not None:
        cached_at, cached_tools = cached
        if now - cached_at < _MCP_CACHE_TTL_SECONDS:
            logger.info(
                "Using cached MCP tools for workspace %s (%d tools, age=%.0fs, bypass_hitl=%s)",
                workspace_id,
                len(cached_tools),
                now - cached_at,
                bypass_internal_hitl,
            )
            return list(cached_tools)

    try:
        # Every connector carrying a ``server_config`` is an MCP server.
        # Cast JSON -> JSONB so we can use has_key to filter by the presence of "server_config".
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.workspace_id == workspace_id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.MCP_CONNECTOR,
                cast(SearchSourceConnector.config, JSONB).has_key("server_config"),
            ),
        )

        connectors = list(result.scalars())

        discovery_tasks: list[dict[str, Any]] = []
        for connector in connectors:
            try:
                cfg = connector.config or {}
                server_config = cfg.get("server_config", {})

                if not server_config or not isinstance(server_config, dict):
                    logger.warning(
                        "MCP connector %d (name: '%s') has invalid or missing server_config, skipping",
                        connector.id,
                        connector.name,
                    )
                    continue

                transport = server_config.get("transport", "stdio")
                if transport not in HTTP_TRANSPORTS:
                    _refuse_non_http(connector.id, connector.name, transport)
                    continue

                trusted_tools = effective_trusted(cfg.get("trusted_tools", []))

                discovery_tasks.append(
                    {
                        "connector_id": connector.id,
                        "connector_name": connector.name,
                        "server_config": server_config,
                        "trusted_tools": trusted_tools,
                        "transport": transport,
                        "cached_tools": read_cached_tools(connector),
                        "origin": origin_of(cfg),
                    }
                )

            except Exception as e:
                logger.exception(
                    "Failed to prepare MCP connector %d: %s",
                    connector.id,
                    e,
                )

        async def _discover_one(task: dict[str, Any]) -> list[StructuredTool]:
            discover_start = time.perf_counter()
            transport = task["transport"]
            cached_tools = task.get("cached_tools")
            try:
                result = await asyncio.wait_for(
                    _load_http_mcp_tools(
                        task["connector_id"],
                        task["connector_name"],
                        task["server_config"],
                        trusted_tools=task["trusted_tools"],
                        bypass_internal_hitl=bypass_internal_hitl,
                        cached_tools=cached_tools,
                        origin=task["origin"],
                    ),
                    timeout=_MCP_DISCOVERY_TIMEOUT_SECONDS,
                )
                _perf_log.info(
                    "[mcp_discover] connector=%s name=%r transport=%s tools=%d elapsed=%.3fs cache=%s",
                    task["connector_id"],
                    task["connector_name"],
                    transport,
                    len(result),
                    time.perf_counter() - discover_start,
                    "hit" if cached_tools is not None else "miss",
                )
                return result
            except TimeoutError:
                _perf_log.info(
                    "[mcp_discover] connector=%s name=%r transport=%s elapsed=%.3fs outcome=timeout",
                    task["connector_id"],
                    task["connector_name"],
                    transport,
                    time.perf_counter() - discover_start,
                )
                logger.error(
                    "MCP connector %d timed out after %ds during discovery",
                    task["connector_id"],
                    _MCP_DISCOVERY_TIMEOUT_SECONDS,
                )
                return []
            except Exception as e:
                _perf_log.info(
                    "[mcp_discover] connector=%s name=%r transport=%s elapsed=%.3fs outcome=error",
                    task["connector_id"],
                    task["connector_name"],
                    transport,
                    time.perf_counter() - discover_start,
                )
                logger.exception(
                    "Failed to load tools from MCP connector %d: %s",
                    task["connector_id"],
                    e,
                )
                return []

        gather_start = time.perf_counter()
        results = await asyncio.gather(*[_discover_one(t) for t in discovery_tasks])
        _perf_log.info(
            "[mcp_discover] gather_wall=%.3fs connectors=%d total_tools=%d",
            time.perf_counter() - gather_start,
            len(discovery_tasks),
            sum(len(r) for r in results),
        )
        tools: list[StructuredTool] = [tool for sublist in results for tool in sublist]

        _mcp_tools_cache[cache_key] = (now, tools)

        if len(_mcp_tools_cache) > _MCP_CACHE_MAX_SIZE:
            oldest_key = min(_mcp_tools_cache, key=lambda k: _mcp_tools_cache[k][0])
            del _mcp_tools_cache[oldest_key]

        logger.info(
            "Loaded %d MCP tools for workspace %d (bypass_hitl=%s)",
            len(tools),
            workspace_id,
            bypass_internal_hitl,
        )
        return tools

    except Exception as e:
        logger.exception("Failed to load MCP tools: %s", e)
        return []
