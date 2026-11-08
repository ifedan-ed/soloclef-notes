import uuid
from datetime import datetime
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, field_validator, model_validator

from app.db import SearchSourceConnectorType
from app.utils.validators import validate_connector_config

from .base import IDModel, TimestampModel


class SearchSourceConnectorBase(BaseModel):
    name: str
    connector_type: SearchSourceConnectorType
    is_indexable: bool
    last_indexed_at: datetime | None = None
    config: dict[str, Any]
    enable_vision_llm: bool = False
    periodic_indexing_enabled: bool = False
    indexing_frequency_minutes: int | None = None
    next_scheduled_at: datetime | None = None

    @field_validator("config")
    @classmethod
    def validate_config_for_connector_type(
        cls,
        config: dict[str, Any],
        values: dict[str, Any],
    ) -> dict[str, Any]:
        connector_type = values.data.get("connector_type")
        return validate_connector_config(connector_type, config)

    @model_validator(mode="after")
    def validate_periodic_indexing(self):
        """Validate that periodic indexing configuration is consistent.

        Supported frequencies: Any positive integer (in minutes).
        Common values: 5, 15, 60 (1 hour), 360 (6 hours), 720 (12 hours), 1440 (daily), etc.
        The schedule checker will handle any frequency >= 1 minute.
        """
        if self.periodic_indexing_enabled:
            if not self.is_indexable:
                msg = "periodic_indexing_enabled can only be True for indexable connectors"
                raise ValueError(
                    msg,
                )
            if self.indexing_frequency_minutes is None:
                msg = "indexing_frequency_minutes is required when periodic_indexing_enabled is True"
                raise ValueError(
                    msg,
                )
            if self.indexing_frequency_minutes <= 0:
                msg = "indexing_frequency_minutes must be greater than 0"
                raise ValueError(msg)
        return self


class SearchSourceConnectorCreate(SearchSourceConnectorBase):
    pass


class SearchSourceConnectorUpdate(BaseModel):
    name: str | None = None
    connector_type: SearchSourceConnectorType | None = None
    is_indexable: bool | None = None
    last_indexed_at: datetime | None = None
    config: dict[str, Any] | None = None
    enable_vision_llm: bool | None = None
    periodic_indexing_enabled: bool | None = None
    indexing_frequency_minutes: int | None = None
    next_scheduled_at: datetime | None = None


class SearchSourceConnectorRead(SearchSourceConnectorBase, IDModel, TimestampModel):
    # NULL for every connector type but MCP_CONNECTOR: account-level
    # connections have no single workspace (custom/ACCOUNT_CONNECTIONS.md).
    workspace_id: int | None
    user_id: uuid.UUID
    # Set only by the list endpoint, which knows which workspace is asking:
    # whether this connection is switched on here, and whether the caller
    # owns it (only the owner may see the switch / edit / delete it).
    enabled_in_workspace: bool | None = None
    is_mine: bool | None = None

    model_config = ConfigDict(from_attributes=True)


# =============================================================================
# MCP-specific schemas
# =============================================================================


_HTTP_TRANSPORTS = ("streamable-http", "http", "sse")


class MCPServerConfig(BaseModel):
    """A remote MCP server: an https URL and the headers that authenticate it.

    Local (stdio) servers are not supported; a config with a ``command``,
    ``args``, ``env`` or ``transport: "stdio"`` is refused. Whether the host
    may be used is decided by the admin allowlist in the routes
    (``app.mcp_security``), not here.
    """

    model_config = ConfigDict(extra="forbid")

    url: str
    headers: dict[str, str] = {}
    transport: Literal["streamable-http", "http", "sse"] = "streamable-http"

    @model_validator(mode="before")
    @classmethod
    def refuse_stdio(cls, data: Any) -> Any:
        if not isinstance(data, dict):
            return data
        data = dict(data)
        # Older clients sent the empty stdio fields along with an HTTP config.
        for key, empty in (("command", (None, "")), ("args", ([],)), ("env", ({},))):
            if key in data:
                if data[key] in empty:
                    data.pop(key)
                else:
                    raise ValueError(STDIO_REFUSED)
        if data.get("transport") == "stdio":
            raise ValueError(STDIO_REFUSED)
        data.pop("name", None)
        return data

    def is_http_transport(self) -> bool:
        return self.transport in _HTTP_TRANSPORTS


STDIO_REFUSED = "Local (stdio) MCP servers are not supported. Use a remote server URL."


class MCPConnectorCreate(BaseModel):
    """Schema for creating an MCP connector."""

    name: str
    server_config: MCPServerConfig  # Single MCP server configuration


class MCPConnectorUpdate(BaseModel):
    """Schema for updating an MCP connector."""

    name: str | None = None
    server_config: MCPServerConfig | None = None


class MCPConnectorRead(BaseModel):
    """Schema for reading an MCP connector with server configs."""

    id: int
    name: str
    connector_type: SearchSourceConnectorType
    # Masked for every reader: header, env and key-like URL values show only
    # their last four characters (``app.mcp_security.secrets``).
    server_config: dict[str, Any]
    workspace_id: int
    user_id: uuid.UUID
    created_at: datetime
    updated_at: datetime

    model_config = ConfigDict(from_attributes=True)

    @classmethod
    def from_connector(cls, connector: SearchSourceConnectorRead) -> "MCPConnectorRead":
        """Convert from base SearchSourceConnectorRead."""
        from app.mcp_security.secrets import mask_server_config

        config = connector.config or {}
        stored = config.get("server_config") or {}
        server_config = mask_server_config(
            {
                key: stored[key]
                for key in ("url", "headers", "transport")
                if key in stored and stored[key] not in (None, "", [], {})
            }
        )

        return cls(
            id=connector.id,
            name=connector.name,
            connector_type=connector.connector_type,
            server_config=server_config,
            workspace_id=connector.workspace_id,
            user_id=connector.user_id,
            created_at=connector.created_at,
            # The connectors table has no updated_at column.
            updated_at=getattr(connector, "updated_at", None) or connector.created_at,
        )
