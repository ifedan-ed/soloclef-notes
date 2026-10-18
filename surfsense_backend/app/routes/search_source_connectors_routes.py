"""
SearchSourceConnector routes for CRUD operations:
POST /search-source-connectors/ - Create a new connector
GET /search-source-connectors/ - List all connectors for the current user (optionally filtered by workspace)
GET /search-source-connectors/{connector_id} - Get a specific connector
PUT /search-source-connectors/{connector_id} - Update a specific connector
DELETE /search-source-connectors/{connector_id} - Delete a specific connector
POST /search-source-connectors/{connector_id}/index - Index content from a connector to a workspace

MCP (Model Context Protocol) Connector routes:
POST /connectors/mcp - Create a new MCP connector with custom API tools
GET /connectors/mcp - List all MCP connectors for the current user's workspace
GET /connectors/mcp/{connector_id} - Get a specific MCP connector with tools config
PUT /connectors/mcp/{connector_id} - Update an MCP connector's tools config
DELETE /connectors/mcp/{connector_id} - Delete an MCP connector

Note: OAuth connectors (Google Drive) support multiple accounts per workspace.
Other non-MCP connectors are limited to one per workspace.
"""

import logging
from datetime import UTC, datetime, timedelta
from typing import Any

from fastapi import APIRouter, Body, Depends, HTTPException, Query
from pydantic import BaseModel, ValidationError
from sqlalchemy import event as sa_event
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.future import select
from sqlalchemy.orm import load_only

from app.auth.context import AuthContext
from app.connectors.email_imap import credentials as email_credentials
from app.db import (
    Permission,
    SearchSourceConnector,
    SearchSourceConnectorEnablement,
    SearchSourceConnectorType,
    get_async_session,
)
from app.knowledge_store.service import record_deleted_documents
from app.mcp_security.allowlist import is_mcp_admin
from app.mcp_security.secrets import (
    MaskedSecretError,
    mask_connector_config,
    restore_masked,
)
from app.mcp_security.server_origin import (
    MEMBER,
    ORIGIN_KEY,
    Origin,
    origin_for_new_server,
    origin_of,
    reachable_hosts_for_save,
)
from app.mcp_security.tool_trust import always_asks
from app.mcp_security.url_guard import MCPDestinationRefusedError, check_destination
from app.notifications.service import NotificationService
from app.observability.analytics import posthog as ph_analytics
from app.observability.signals import tracing
from app.schemas import (
    GoogleDriveIndexRequest,
    MCPConnectorCreate,
    MCPConnectorRead,
    MCPConnectorUpdate,
    MCPServerConfig,
    SearchSourceConnectorBase,
    SearchSourceConnectorCreate,
    SearchSourceConnectorRead,
    SearchSourceConnectorUpdate,
)
from app.services.connector_enablement import (
    get_enablement_row,
    is_enabled_in_workspace,
    set_enabled_in_workspace,
)
from app.services.connector_service import invalidate_connector_discovery_cache
from app.users import get_auth_context, require_session_context
from app.utils.connector_naming import ensure_unique_connector_name
from app.utils.periodic_scheduler import (
    create_periodic_schedule,
    delete_periodic_schedule,
    update_periodic_schedule,
)
from app.utils.rbac import check_permission, check_workspace_access
from app.utils.validators import raise_if_connector_deprecated

# Set up logging
logger = logging.getLogger(__name__)

router = APIRouter()


def _connector_type_value(connector_type) -> str:
    """Low-cardinality connector_type string for analytics (enum -> its value)."""
    return getattr(connector_type, "value", None) or str(connector_type)


def _emit_connector_connected(_mapper, _connection, target) -> None:
    """SQLAlchemy after_insert hook: one guard for every connector-creation path.

    Fires ``connector_connected`` for the form route here AND every other
    creation path (each builds ``SearchSourceConnector(...)`` inline with no
    shared helper), so no call site is instrumented. ``distinct_id`` comes
    off the row itself (``user_id``) — the same person the web app identifies.

    ponytail: fires during flush, so a post-flush rollback would over-count
    (rare; connector creation commits immediately after add). No-op without a
    PostHog key. Upgrade path: move to an after_commit collector if over-count
    ever shows up in the data.
    """
    if not ph_analytics.is_enabled():
        return
    user_id = getattr(target, "user_id", None)
    if not user_id:
        return
    workspace_id = getattr(target, "workspace_id", None)
    ph_analytics.capture(
        "connector_connected",
        distinct_id=str(user_id),
        properties={
            "workspace_id": workspace_id,
            "connector_id": target.id,
            "connector_type": _connector_type_value(target.connector_type),
        },
        groups={"workspace": str(workspace_id)} if workspace_id is not None else None,
    )


sa_event.listen(SearchSourceConnector, "after_insert", _emit_connector_connected)


def _refuse_pasted_nextcloud_login() -> None:
    """Nextcloud is connected with its Login Flow, never with a pasted password."""
    raise HTTPException(
        status_code=400,
        detail="Connect Nextcloud with the Connect button in Connectors",
    )


_EMAIL_IMAP = SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR


def _refuse_mailboxes_via_generic_route() -> None:
    """Email (IMAP) mailboxes are added only through Connectors > Email (IMAP).

    Those routes build each record themselves (a password typed there, or a
    Microsoft sign-in). The generic routes never write ``config.accounts``:
    a record copied from someone else's connection would carry their
    encrypted password or Microsoft token, which the server would decrypt
    and use (custom/docs/email-imap-connector.md, "Who can see what").
    """
    raise HTTPException(
        status_code=400,
        detail="Add mailboxes from Connectors > Email (IMAP)",
    )


# =============================================================================
# MCP guards: who may add or edit a server, where it may point, what readers see
# =============================================================================

_MCP_TEST_LIMIT = 10
_MCP_TEST_WINDOW_SECONDS = 60.0
_mcp_test_calls: dict[str, list[float]] = {}


async def _readable(
    connector: SearchSourceConnector,
    *,
    session: AsyncSession | None = None,
    workspace_id: int | None = None,
    caller_user_id=None,
) -> SearchSourceConnectorRead:
    """The connector as any reader may see it: MCP credentials masked, and
    Email (IMAP) mailboxes without their encrypted passwords or tokens.

    When ``session`` + ``workspace_id`` are given for a non-MCP connector,
    the periodic/indexing fields in the response come from that workspace's
    enablement row instead of the connector's own (unused, vestigial for
    non-MCP) columns — see custom/ACCOUNT_CONNECTIONS.md. ``caller_user_id``
    additionally sets ``is_mine``, and (with ``workspace_id``)
    ``enabled_in_workspace`` — both for the UI's on/off switch, which only
    the owner sees.
    """
    read = SearchSourceConnectorRead.model_validate(connector)
    if isinstance((read.config or {}).get("server_config"), dict):
        read = read.model_copy(update={"config": mask_connector_config(read.config)})
    if connector.connector_type == _EMAIL_IMAP:
        # Mailboxes without their encrypted passwords/tokens, for every
        # caller (owner included): see email_credentials.public_config.
        read = read.model_copy(
            update={"config": email_credentials.public_config(read.config)}
        )
    if caller_user_id is not None:
        read = read.model_copy(update={"is_mine": connector.user_id == caller_user_id})
    if (
        workspace_id is not None
        and session is not None
        and connector.connector_type != SearchSourceConnectorType.MCP_CONNECTOR
    ):
        row = await get_enablement_row(session, connector.id, workspace_id)
        enabled_here = await is_enabled_in_workspace(session, connector, workspace_id)
        read = read.model_copy(
            update={
                "enabled_in_workspace": enabled_here,
                "is_indexable": row.is_indexable if row else False,
                "periodic_indexing_enabled": row.periodic_indexing_enabled
                if row
                else False,
                "indexing_frequency_minutes": row.indexing_frequency_minutes
                if row
                else None,
                "next_scheduled_at": row.next_scheduled_at if row else None,
                "last_indexed_at": row.last_indexed_at
                if row
                else connector.last_indexed_at,
            }
        )
    return read


def _require_interactive_session(auth: AuthContext) -> None:
    """Adding an MCP server: a signed-in person, never an API key or agent.

    Who may add one in a workspace is its CONNECTORS_CREATE permission (the
    Owner and Editor roles), checked by the route; an administrator's server
    is a site server held to the allow-list, anyone else's is their own
    (``app.mcp_security.server_origin``).
    """
    if not auth.is_session:
        raise HTTPException(
            status_code=403, detail="This action requires an interactive session"
        )


def _require_mcp_editor(auth: AuthContext, connector: SearchSourceConnector) -> None:
    """Editing an MCP server: the person who added it, or an administrator."""
    if not auth.is_session:
        raise HTTPException(
            status_code=403, detail="This action requires an interactive session"
        )
    if connector.user_id != auth.user.id and not is_mcp_admin(auth.user):
        raise HTTPException(
            status_code=403,
            detail="Only the person who added this MCP server or an administrator can change it",
        )


async def _require_mcp_remover(
    session: AsyncSession, auth: AuthContext, connector: SearchSourceConnector
) -> None:
    """Deleting an MCP server: ``connectors:delete`` in its workspace (the
    Owner), or the person who added it on their own key (a member server)
    while they are a member, so an Editor can always stop their own key
    being spent. Site servers stay the Owner's to remove."""
    if connector.user_id == auth.user.id and origin_of(connector.config) == MEMBER:
        await check_workspace_access(session, auth, connector.workspace_id)
        return
    await check_permission(
        session,
        auth,
        connector.workspace_id,
        Permission.CONNECTORS_DELETE.value,
        "You don't have permission to delete this connector",
    )


def _validation_detail(exc: ValidationError) -> str:
    errors = exc.errors()
    if errors:
        msg = str(errors[0].get("msg", "Invalid MCP server configuration"))
        return msg.removeprefix("Value error, ")
    return "Invalid MCP server configuration"


async def _validated_mcp_server_config(
    session: AsyncSession,
    incoming: MCPServerConfig | dict[str, Any],
    stored: dict[str, Any] | None,
    origin: Origin,
) -> dict[str, Any]:
    """Schema, masked-secret restore and destination checks, in that order.

    ``origin`` decides the destination rule: a site server's host must be on
    the allow-list, a member's own server may be any public https host.
    """
    try:
        cfg = (
            incoming
            if isinstance(incoming, MCPServerConfig)
            else MCPServerConfig.model_validate(incoming)
        )
    except ValidationError as exc:
        raise HTTPException(status_code=422, detail=_validation_detail(exc)) from exc
    try:
        data = restore_masked(cfg.model_dump(), stored)
    except MaskedSecretError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    try:
        hosts = await reachable_hosts_for_save(session, origin, data["url"])
        await check_destination(data["url"], hosts)
    except MCPDestinationRefusedError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return data


def _check_mcp_test_rate(user_id: Any) -> None:
    """At most ``_MCP_TEST_LIMIT`` connection tests a minute per person."""
    import time

    now = time.monotonic()
    key = str(user_id)
    recent = [
        t for t in _mcp_test_calls.get(key, []) if now - t < _MCP_TEST_WINDOW_SECONDS
    ]
    if len(recent) >= _MCP_TEST_LIMIT:
        _mcp_test_calls[key] = recent
        raise HTTPException(status_code=429, detail="RATE_LIMIT_EXCEEDED")
    recent.append(now)
    _mcp_test_calls[key] = recent
    if len(_mcp_test_calls) > 10_000:
        _mcp_test_calls.clear()


def _refuse_mcp_via_generic_route() -> None:
    raise HTTPException(
        status_code=400,
        detail="Add MCP servers from Connectors > MCP server",
    )


@router.post("/search-source-connectors", response_model=SearchSourceConnectorRead)
async def create_search_source_connector(
    connector: SearchSourceConnectorCreate,
    workspace_id: int = Query(
        ..., description="ID of the workspace to associate the connector with"
    ),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Create a new search source connector.
    Requires CONNECTORS_CREATE permission.

    Each workspace can have only one connector of each type (based on workspace_id and connector_type).
    The config must contain the appropriate keys for the connector type.
    """
    try:
        # Refuse new connections for deprecated connector types (HTTP 410).
        raise_if_connector_deprecated(connector.connector_type)

        # MCP servers are added only through /connectors/mcp (its own checks:
        # Owner/Editor, https, public address; allow-list for site servers).
        if (
            connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR
            or "server_config" in (connector.config or {})
        ):
            _refuse_mcp_via_generic_route()

        # Check if user has permission to create connectors
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.CONNECTORS_CREATE.value,
            "You don't have permission to create connectors in this workspace",
        )

        is_mcp = connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR

        if connector.connector_type == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR:
            _refuse_pasted_nextcloud_login()
        if connector.connector_type == _EMAIL_IMAP and (connector.config or {}).get(
            "accounts"
        ):
            _refuse_mailboxes_via_generic_route()

        if is_mcp:
            # Check if a connector with the same type already exists for this
            # workspace. MCP_CONNECTOR can have multiple instances with
            # different names, so it's excluded.
            result = await session.execute(
                select(SearchSourceConnector).filter(
                    SearchSourceConnector.workspace_id == workspace_id,
                    SearchSourceConnector.connector_type == connector.connector_type,
                )
            )
            if result.scalars().first():
                raise HTTPException(
                    status_code=409,
                    detail=f"A connector with type {connector.connector_type} already exists in this workspace.",
                )
            connector_data = connector.model_dump()
            connector_data["name"] = await ensure_unique_connector_name(
                session, connector_data["name"], workspace_id, user.id
            )
            if (
                connector.periodic_indexing_enabled
                and connector.indexing_frequency_minutes
                and connector.next_scheduled_at is None
            ):
                connector_data["next_scheduled_at"] = datetime.now(UTC) + timedelta(
                    minutes=connector.indexing_frequency_minutes
                )
            db_connector = SearchSourceConnector(
                **connector_data, workspace_id=workspace_id, user_id=user.id
            )
            session.add(db_connector)
            await session.commit()
            await session.refresh(db_connector)
        else:
            # Account-level: reuse this account's connector of the same type
            # + name if one exists (custom/ACCOUNT_CONNECTIONS.md), else
            # create it with no single workspace. Periodic-indexing fields
            # go on this workspace's enablement row, not the connector.
            connector_data = connector.model_dump()
            for periodic_field in (
                "periodic_indexing_enabled",
                "indexing_frequency_minutes",
                "next_scheduled_at",
                "is_indexable",
            ):
                connector_data.pop(periodic_field, None)
            result = await session.execute(
                select(SearchSourceConnector).filter(
                    SearchSourceConnector.workspace_id.is_(None),
                    SearchSourceConnector.user_id == user.id,
                    SearchSourceConnector.connector_type == connector.connector_type,
                    SearchSourceConnector.name == connector_data["name"],
                )
            )
            db_connector = result.scalars().first()
            if db_connector is None:
                db_connector = SearchSourceConnector(
                    **connector_data, workspace_id=None, user_id=user.id
                )
                session.add(db_connector)
                await session.commit()
                await session.refresh(db_connector)

            from app.services.connector_enablement import set_enabled_in_workspace

            enablement = await set_enabled_in_workspace(
                session,
                connector=db_connector,
                workspace_id=workspace_id,
                enabled=True,
                enabled_by_user_id=user.id,
            )
            enablement.is_indexable = connector.is_indexable
            if (
                connector.periodic_indexing_enabled
                and connector.indexing_frequency_minutes
                and connector.next_scheduled_at is None
            ):
                enablement.next_scheduled_at = datetime.now(UTC) + timedelta(
                    minutes=connector.indexing_frequency_minutes
                )
            else:
                enablement.next_scheduled_at = connector.next_scheduled_at
            enablement.periodic_indexing_enabled = connector.periodic_indexing_enabled
            enablement.indexing_frequency_minutes = connector.indexing_frequency_minutes
            await session.commit()
            invalidate_connector_discovery_cache(workspace_id)

        # ``connector_connected`` is emitted by the after_insert listener below,
        # so it fires once for this form route AND every other creation path
        # without instrumenting each — see _emit_connector_connected.

        # Create periodic schedule if periodic indexing is enabled (connector
        # field for MCP, this workspace's enablement for every other type —
        # the request's own values cover both since they're set from it above)
        if connector.periodic_indexing_enabled and connector.indexing_frequency_minutes:
            success = create_periodic_schedule(
                connector_id=db_connector.id,
                workspace_id=workspace_id,
                user_id=str(user.id),
                connector_type=db_connector.connector_type,
                frequency_minutes=db_connector.indexing_frequency_minutes,
                connector_config=db_connector.config,
            )
            if not success:
                logger.warning(
                    f"Failed to create periodic schedule for connector {db_connector.id}"
                )

        return await _readable(
            db_connector,
            session=session,
            workspace_id=workspace_id if not is_mcp else None,
        )
    except ValidationError as e:
        await session.rollback()
        raise HTTPException(status_code=422, detail=f"Validation error: {e!s}") from e
    except IntegrityError as e:
        await session.rollback()
        raise HTTPException(
            status_code=409,
            detail=f"Integrity error: A connector with this type already exists in this workspace. {e!s}",
        ) from e
    except HTTPException:
        await session.rollback()
        raise
    except Exception as e:
        logger.error(f"Failed to create search source connector: {e!s}")
        await session.rollback()
        raise HTTPException(
            status_code=500,
            detail=f"Failed to create search source connector: {e!s}",
        ) from e


@router.get("/search-source-connectors", response_model=list[SearchSourceConnectorRead])
async def read_search_source_connectors(
    skip: int = 0,
    limit: int = 100,
    workspace_id: int | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    List all search source connectors for a workspace.
    Requires CONNECTORS_READ permission.
    """
    try:
        if workspace_id is None:
            raise HTTPException(
                status_code=400,
                detail="workspace_id is required",
            )

        # Check if user has permission to read connectors
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.CONNECTORS_READ.value,
            "You don't have permission to view connectors in this workspace",
        )

        # MCP stays workspace-scoped; every other connector type is
        # account-level, enabled here by default (custom/ACCOUNT_CONNECTIONS.md).
        # The list is what this workspace's connections page shows: every
        # connection enabled here (any owner, usable by every member) plus
        # the caller's own even if they've switched it off here — the
        # owner's on/off switch needs to see it to turn it back on.
        from app.services.connector_enablement import enabled_connectors_in_workspace

        enabled_here = await enabled_connectors_in_workspace(session, workspace_id)
        own_result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.user_id == auth.user.id,
                SearchSourceConnector.workspace_id.is_(None),
            )
        )
        by_id = {c.id: c for c in enabled_here}
        for c in own_result.scalars().all():
            by_id[c.id] = c
        mcp_result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.workspace_id == workspace_id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.MCP_CONNECTOR,
            )
        )
        for c in mcp_result.scalars().all():
            by_id[c.id] = c
        connectors = list(by_id.values())[skip : skip + limit]
        return [
            await _readable(
                c,
                session=session,
                workspace_id=workspace_id,
                caller_user_id=auth.user.id,
            )
            for c in connectors
        ]
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"Failed to fetch search source connectors: {e!s}",
        ) from e


@router.get(
    "/search-source-connectors/{connector_id}", response_model=SearchSourceConnectorRead
)
async def read_search_source_connector(
    connector_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Get a specific search source connector by ID.
    Requires CONNECTORS_READ permission.
    """
    try:
        # Get the connector first
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.id == connector_id
            )
        )
        connector = result.scalars().first()

        if not connector:
            raise HTTPException(status_code=404, detail="Connector not found")

        if connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR:
            await check_permission(
                session,
                auth,
                connector.workspace_id,
                Permission.CONNECTORS_READ.value,
                "You don't have permission to view this connector",
            )
        elif connector.user_id != auth.user.id:
            # Account-level connection, fetched directly by id: full details
            # (including config) are the owner's alone. A co-member sees it
            # only through the workspace list, never this route.
            raise HTTPException(status_code=404, detail="Connector not found")

        return await _readable(connector, session=session)
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to fetch search source connector: {e!s}"
        ) from e


@router.put(
    "/search-source-connectors/{connector_id}", response_model=SearchSourceConnectorRead
)
async def update_search_source_connector(
    connector_id: int,
    connector_update: SearchSourceConnectorUpdate,
    workspace_id: int | None = Query(
        default=None,
        description=(
            "Required when the update includes is_indexable, "
            "periodic_indexing_enabled, indexing_frequency_minutes or "
            "next_scheduled_at for a non-MCP connector — those are this "
            "workspace's enablement settings, not the (account-level) "
            "connector's own fields."
        ),
    ),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Update a search source connector.
    Requires CONNECTORS_UPDATE permission.
    Handles partial updates, including merging changes into the 'config' field.
    """
    # Get the connector first
    result = await session.execute(
        select(SearchSourceConnector).filter(SearchSourceConnector.id == connector_id)
    )
    db_connector = result.scalars().first()

    if not db_connector:
        raise HTTPException(status_code=404, detail="Connector not found")

    is_mcp = db_connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR
    if is_mcp:
        await check_permission(
            session,
            auth,
            db_connector.workspace_id,
            Permission.CONNECTORS_UPDATE.value,
            "You don't have permission to update this connector",
        )
    elif db_connector.user_id != auth.user.id:
        # Account-level connection: only its owner may edit it
        # (custom/ACCOUNT_CONNECTIONS.md).
        raise HTTPException(
            status_code=403,
            detail="Only this connection's owner can edit it",
        )

    # Convert the sparse update data (only fields present in request) to a dict
    update_data = connector_update.model_dump(exclude_unset=True)
    mcp_config_changed = False
    if is_mcp:
        _require_mcp_editor(auth, db_connector)
        if update_data.get("connector_type") not in (
            None,
            SearchSourceConnectorType.MCP_CONNECTOR,
        ):
            _refuse_mcp_via_generic_route()
        update_data.pop("connector_type", None)
        if "config" in update_data:
            incoming = update_data.pop("config") or {}
            if "server_config" in incoming:
                existing = dict(db_connector.config or {})
                existing["server_config"] = await _validated_mcp_server_config(
                    session,
                    incoming["server_config"] or {},
                    (db_connector.config or {}).get("server_config"),
                    origin_of(db_connector.config),
                )
                existing.pop("auth_expired", None)
                update_data["config"] = existing
                mcp_config_changed = True
    elif update_data.get(
        "connector_type"
    ) == SearchSourceConnectorType.MCP_CONNECTOR or "server_config" in (
        update_data.get("config") or {}
    ):
        _refuse_mcp_via_generic_route()

    # A connection can't be turned into (or out of) Email (IMAP) here: a
    # config written under another type would become stored mailboxes.
    if update_data.get("connector_type") not in (
        None,
        db_connector.connector_type,
    ) and _EMAIL_IMAP in (update_data["connector_type"], db_connector.connector_type):
        _refuse_mailboxes_via_generic_route()

    # Non-MCP: is_indexable/periodic-indexing fields are this *workspace's*
    # enablement settings, not the (account-level) connector's own columns —
    # see custom/ACCOUNT_CONNECTIONS.md. Pull them out of update_data (so
    # they never land on db_connector's now-vestigial fields) and validate/
    # apply them against the enablement row instead.
    enablement_update: dict[str, Any] = {}
    enablement_row = None
    if not is_mcp:
        for field in (
            "is_indexable",
            "periodic_indexing_enabled",
            "indexing_frequency_minutes",
            "next_scheduled_at",
        ):
            if field in update_data:
                enablement_update[field] = update_data.pop(field)
        if enablement_update:
            if workspace_id is None:
                raise HTTPException(
                    status_code=400,
                    detail=(
                        "workspace_id is required to change is_indexable, "
                        "periodic_indexing_enabled, indexing_frequency_minutes "
                        "or next_scheduled_at for this connector"
                    ),
                )
            # These settings are workspace_id's own, so they may only be
            # written where the connector is usable (its owner a member, not
            # switched off there; 404 like the index route, so no other
            # workspace's existence shows) and by someone who may update
            # connectors there. Without both, an owner could schedule their
            # account's files into any workspace by its id.
            if not await is_enabled_in_workspace(session, db_connector, workspace_id):
                raise HTTPException(status_code=404, detail="Connector not found")
            await check_permission(
                session,
                auth,
                workspace_id,
                Permission.CONNECTORS_UPDATE.value,
                "You don't have permission to update connectors in this workspace",
            )
            enablement_row = await get_enablement_row(
                session, db_connector.id, workspace_id
            )

    def _enablement_default(field: str):
        return getattr(enablement_row, field) if enablement_row is not None else False

    # Validate periodic indexing fields
    # Get the effective values after update
    if is_mcp:
        effective_is_indexable = update_data.get(
            "is_indexable", db_connector.is_indexable
        )
        effective_periodic_enabled = update_data.get(
            "periodic_indexing_enabled", db_connector.periodic_indexing_enabled
        )
        effective_frequency = update_data.get(
            "indexing_frequency_minutes", db_connector.indexing_frequency_minutes
        )
    else:
        effective_is_indexable = enablement_update.get(
            "is_indexable", _enablement_default("is_indexable")
        )
        effective_periodic_enabled = enablement_update.get(
            "periodic_indexing_enabled",
            _enablement_default("periodic_indexing_enabled"),
        )
        effective_frequency = enablement_update.get(
            "indexing_frequency_minutes",
            enablement_row.indexing_frequency_minutes if enablement_row else None,
        )

    # Validate periodic indexing configuration
    if effective_periodic_enabled:
        if not effective_is_indexable:
            raise HTTPException(
                status_code=422,
                detail="periodic_indexing_enabled can only be True for indexable connectors",
            )
        if effective_frequency is None:
            raise HTTPException(
                status_code=422,
                detail="indexing_frequency_minutes is required when periodic_indexing_enabled is True",
            )
        if effective_frequency <= 0:
            raise HTTPException(
                status_code=422,
                detail="indexing_frequency_minutes must be greater than 0",
            )

        # Automatically set next_scheduled_at if not provided and periodic indexing is being enabled
        _target = update_data if is_mcp else enablement_update
        if (
            "periodic_indexing_enabled" in _target
            or "indexing_frequency_minutes" in _target
        ) and "next_scheduled_at" not in _target:
            # Schedule the next indexing based on the frequency
            _target["next_scheduled_at"] = datetime.now(UTC) + timedelta(
                minutes=effective_frequency
            )
    elif effective_periodic_enabled is False and "periodic_indexing_enabled" in (
        update_data if is_mcp else enablement_update
    ):
        # If disabling periodic indexing, clear the next_scheduled_at
        (update_data if is_mcp else enablement_update)["next_scheduled_at"] = None

    # Special handling for 'config' field (MCP configs were settled above)
    if "config" in update_data and not is_mcp:
        incoming_config = update_data["config"]  # Config data from the request
        existing_config = (
            db_connector.config if db_connector.config else {}
        )  # Current config from DB

        # Merge incoming config into existing config
        # This preserves existing keys (like GITHUB_PAT) if they are not in the incoming data
        merged_config = existing_config.copy()
        merged_config.update(incoming_config)

        # -- Validation after merging --
        # Validate the *merged* config based on the connector type
        # We need the connector type - use the one from the update if provided, else the existing one
        current_connector_type = (
            connector_update.connector_type
            if connector_update.connector_type is not None
            else db_connector.connector_type
        )
        if current_connector_type == _EMAIL_IMAP:
            # Other config keys may change; the stored mailboxes never do
            # (email_credentials.keep_stored_accounts).
            merged_config = email_credentials.keep_stored_accounts(
                merged_config, existing_config
            )

        try:
            # We can reuse the base validator by creating a temporary base model instance
            # Note: This assumes 'name' and 'is_indexable' are not crucial for config validation itself
            temp_data_for_validation = {
                "name": db_connector.name,  # Use existing name
                "connector_type": current_connector_type,
                "is_indexable": db_connector.is_indexable,  # Use existing value
                "last_indexed_at": db_connector.last_indexed_at,  # Not used by validator
                "config": merged_config,
            }
            SearchSourceConnectorBase.model_validate(temp_data_for_validation)
        except ValidationError as e:
            # Raise specific validation error for the merged config
            raise HTTPException(
                status_code=422, detail=f"Validation error for merged config: {e!s}"
            ) from e

        if current_connector_type == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR:
            # Only the Login Flow writes a Nextcloud login; a rename keeps it.
            merged_config = existing_config

        # If validation passes, update the main update_data dict with the merged config
        update_data["config"] = merged_config

    # Apply all updates (including the potentially merged config)
    for key, value in update_data.items():
        # Prevent changing connector_type if it causes a duplicate (check moved here)
        if key == "connector_type" and value != db_connector.connector_type:
            scope = (
                SearchSourceConnector.workspace_id == db_connector.workspace_id
                if is_mcp
                else SearchSourceConnector.workspace_id.is_(None)
            )
            check_result = await session.execute(
                select(SearchSourceConnector).filter(
                    scope,
                    SearchSourceConnector.user_id == db_connector.user_id,
                    SearchSourceConnector.connector_type == value,
                    SearchSourceConnector.id != connector_id,
                )
            )
            existing_connector = check_result.scalars().first()
            if existing_connector:
                raise HTTPException(
                    status_code=409,
                    detail=f"A connector with type {value} already exists in this workspace.",
                )

        setattr(db_connector, key, value)

    try:
        await session.commit()
        await session.refresh(db_connector)

        if enablement_update:
            if enablement_row is None:
                enablement_row = await get_enablement_row(
                    session, db_connector.id, workspace_id
                )
            if enablement_row is None:
                enablement_row = SearchSourceConnectorEnablement(
                    connector_id=db_connector.id,
                    workspace_id=workspace_id,
                    enabled_by_user_id=user.id,
                )
                session.add(enablement_row)
            for field, value in enablement_update.items():
                setattr(enablement_row, field, value)
            await session.commit()
            invalidate_connector_discovery_cache(workspace_id)

        # Handle periodic schedule updates (logging only today; see
        # app/utils/periodic_scheduler.py)
        if is_mcp:
            if (
                "periodic_indexing_enabled" in update_data
                or "indexing_frequency_minutes" in update_data
            ):
                if (
                    db_connector.periodic_indexing_enabled
                    and db_connector.indexing_frequency_minutes
                ):
                    success = update_periodic_schedule(
                        connector_id=db_connector.id,
                        workspace_id=db_connector.workspace_id,
                        user_id=str(user.id),
                        connector_type=db_connector.connector_type,
                        frequency_minutes=db_connector.indexing_frequency_minutes,
                    )
                else:
                    success = delete_periodic_schedule(db_connector.id)
                if not success:
                    logger.warning(
                        f"Failed to update periodic schedule for connector {db_connector.id}"
                    )
        elif enablement_update and (
            "periodic_indexing_enabled" in enablement_update
            or "indexing_frequency_minutes" in enablement_update
        ):
            if (
                enablement_row.periodic_indexing_enabled
                and enablement_row.indexing_frequency_minutes
            ):
                success = update_periodic_schedule(
                    connector_id=db_connector.id,
                    workspace_id=workspace_id,
                    user_id=str(user.id),
                    connector_type=db_connector.connector_type,
                    frequency_minutes=enablement_row.indexing_frequency_minutes,
                )
            else:
                success = delete_periodic_schedule(db_connector.id)
            if not success:
                logger.warning(
                    f"Failed to update periodic schedule for connector {db_connector.id} "
                    f"in workspace {workspace_id}"
                )

        if mcp_config_changed:
            from app.agents.chat.multi_agent_chat.shared.tools.mcp.cache import (
                refresh_mcp_tools_cache_for_connector,
            )

            refresh_mcp_tools_cache_for_connector(
                db_connector.id, db_connector.workspace_id
            )

        return await _readable(
            db_connector,
            session=session,
            workspace_id=workspace_id if not is_mcp else None,
        )
    except IntegrityError as e:
        await session.rollback()
        # This might occur if connector_type constraint is violated somehow after the check
        raise HTTPException(
            status_code=409, detail=f"Database integrity error during update: {e!s}"
        ) from e
    except Exception as e:
        await session.rollback()
        logger.error(
            f"Failed to update search source connector {connector_id}: {e}",
            exc_info=True,
        )
        raise HTTPException(
            status_code=500,
            detail=f"Failed to update search source connector: {e!s}",
        ) from e


@router.delete("/search-source-connectors/{connector_id}", response_model=dict)
async def delete_search_source_connector(
    connector_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Delete a search source connector and all its associated documents.

    The deletion happens inline (documents are deleted in batches,
    then the connector record is removed).

    Requires CONNECTORS_DELETE permission.
    """
    from sqlalchemy import delete as sa_delete, func

    from app.db import Document

    deletion_batch_size = 500

    try:
        # Get the connector first
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.id == connector_id
            )
        )
        db_connector = result.scalars().first()

        if not db_connector:
            raise HTTPException(status_code=404, detail="Connector not found")

        if db_connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR:
            # Workspace-scoped, as before: a workspace admin may delete
            # someone else's MCP tool; the adder may delete their own.
            await _require_mcp_remover(session, auth, db_connector)
        elif db_connector.user_id != auth.user.id:
            # Account-level connection: this disconnects it everywhere the
            # owner uses it, so only the owner may do it (see
            # custom/ACCOUNT_CONNECTIONS.md, "only the connector's owner
            # can edit, delete, or switch it on/off").
            raise HTTPException(
                status_code=403,
                detail="Only this connection's owner can delete it",
            )

        # Store connector info before deletion
        connector_name = db_connector.name

        # Delete any periodic schedule associated with this connector
        if db_connector.periodic_indexing_enabled:
            success = delete_periodic_schedule(connector_id)
            if not success:
                logger.warning(
                    f"Failed to delete periodic schedule for connector {connector_id}"
                )

        if (
            db_connector.connector_type
            == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR
        ):
            # Revoke the grant at Google too (best effort).
            from app.connectors.google_drive.connect.revocation import (
                revoke_stored_grant,
            )

            await revoke_stored_grant(db_connector.config)

        if db_connector.connector_type == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR:
            # Retire the app password in Nextcloud too (best effort).
            from app.connectors.nextcloud.login_flow.revocation import (
                revoke_stored_password,
            )

            await revoke_stored_password(db_connector.config)

        # Delete documents in batches (chunks are deleted via CASCADE)
        total_deleted = 0
        count_result = await session.execute(
            select(func.count(Document.id)).where(Document.connector_id == connector_id)
        )
        total_docs = count_result.scalar() or 0

        logger.info(
            f"Starting deletion of connector {connector_id} ({connector_name}). "
            f"Documents to delete: {total_docs}"
        )

        while True:
            result = await session.execute(
                select(Document)
                .options(
                    # Only what the store needs to find each file. The content
                    # columns are the large ones and this batch is 500 rows.
                    load_only(
                        Document.title,
                        Document.folder_id,
                        Document.workspace_id,
                        Document.document_metadata,
                    )
                )
                .where(Document.connector_id == connector_id)
                .limit(deletion_batch_size)
            )
            documents = list(result.scalars().all())

            if not documents:
                break

            # Before the rows go: each is what says where its file is.
            # ponytail: one path index per batch, so a connector with many
            # thousands of documents rebuilds it repeatedly. Upgrade path is an
            # index the caller builds once and hands in.
            await record_deleted_documents(session, documents)

            doc_ids = [document.id for document in documents]
            await session.execute(sa_delete(Document).where(Document.id.in_(doc_ids)))
            await session.commit()

            total_deleted += len(doc_ids)
            logger.info(
                f"Deleted batch of {len(doc_ids)} documents. "
                f"Progress: {total_deleted}/{total_docs}"
            )

        # Delete the connector record
        workspace_id = db_connector.workspace_id
        deleted_connector_type = db_connector.connector_type
        is_mcp = db_connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR
        await session.delete(db_connector)
        await session.commit()
        # Account-level delete can affect every workspace the owner belongs
        # to at once (not just `workspace_id`, which is only meaningful for
        # MCP); clear the whole discovery cache rather than one entry.
        invalidate_connector_discovery_cache()

        # Authoritative deletion (migrated from use-connector-dialog.ts).
        ph_analytics.capture_for(
            auth,
            "connector_deleted",
            {
                "workspace_id": workspace_id,
                "connector_id": connector_id,
                "connector_type": _connector_type_value(deleted_connector_type),
            },
            groups={"workspace": str(workspace_id)},
        )

        if is_mcp:
            from app.agents.chat.multi_agent_chat.shared.tools.mcp.tool import (
                invalidate_mcp_tools_cache,
            )

            invalidate_mcp_tools_cache(workspace_id)

        logger.info(
            f"Connector {connector_id} ({connector_name}) deleted successfully. "
            f"Total documents deleted: {total_deleted}"
        )

        doc_text = "document" if total_deleted == 1 else "documents"
        return {
            "message": f"Connector '{connector_name}' deleted. {total_deleted} {doc_text} removed.",
            "status": "completed",
            "connector_id": connector_id,
            "connector_name": connector_name,
            "documents_deleted": total_deleted,
        }
    except HTTPException:
        raise
    except Exception as e:
        await session.rollback()
        raise HTTPException(
            status_code=500,
            detail=f"Failed to start connector deletion: {e!s}",
        ) from e


async def _owned_non_mcp_connector(
    session: AsyncSession, auth: AuthContext, connector_id: int
) -> SearchSourceConnector:
    """The caller's own account-level connection, or 404.

    Enable/disable only makes sense for the account model — MCP connectors
    are still created directly in one workspace and have no enablement row.
    """
    connector = await session.get(SearchSourceConnector, connector_id)
    if (
        connector is None
        or connector.connector_type == SearchSourceConnectorType.MCP_CONNECTOR
    ):
        raise HTTPException(status_code=404, detail="Connector not found")
    if connector.user_id != auth.user.id:
        raise HTTPException(
            status_code=403,
            detail="Only this connection's owner can change where it's enabled",
        )
    return connector


@router.post(
    "/search-source-connectors/{connector_id}/workspaces/{workspace_id}",
    response_model=dict,
)
async def enable_connector_in_workspace(
    connector_id: int,
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Switch a connection on in a workspace (a no-op if it's already on —
    enabled is the default; this only clears a prior explicit disable)."""
    connector = await _owned_non_mcp_connector(session, auth, connector_id)
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to enable connectors in this workspace",
    )
    await set_enabled_in_workspace(
        session,
        connector=connector,
        workspace_id=workspace_id,
        enabled=True,
        enabled_by_user_id=auth.user.id,
    )
    await session.commit()
    invalidate_connector_discovery_cache(workspace_id)
    return {"connector_id": connector_id, "workspace_id": workspace_id, "enabled": True}


@router.delete(
    "/search-source-connectors/{connector_id}/workspaces/{workspace_id}",
    response_model=dict,
)
async def disable_connector_in_workspace(
    connector_id: int,
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Switch a connection off in one workspace and remove what it indexed there.

    The connector itself, its credentials, and every other workspace's use
    of it are untouched — this is the opposite of deleting the connector.
    """
    from sqlalchemy import delete as sa_delete

    from app.db import Document

    connector = await _owned_non_mcp_connector(session, auth, connector_id)
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_DELETE.value,
        "You don't have permission to disable connectors in this workspace",
    )
    await set_enabled_in_workspace(
        session,
        connector=connector,
        workspace_id=workspace_id,
        enabled=False,
        enabled_by_user_id=auth.user.id,
    )
    result = await session.execute(
        select(Document)
        .options(
            load_only(
                Document.title,
                Document.folder_id,
                Document.workspace_id,
                Document.document_metadata,
            )
        )
        .where(
            Document.connector_id == connector_id,
            Document.workspace_id == workspace_id,
        )
    )
    documents = list(result.scalars().all())
    if documents:
        await record_deleted_documents(session, documents)
        await session.execute(
            sa_delete(Document).where(
                Document.id.in_([d.id for d in documents]),
            )
        )
    await session.commit()
    invalidate_connector_discovery_cache(workspace_id)
    return {
        "connector_id": connector_id,
        "workspace_id": workspace_id,
        "enabled": False,
        "documents_removed": len(documents),
    }


@router.get(
    "/search-source-connectors/{connector_id}/workspaces/{workspace_id}",
    response_model=dict,
)
async def connector_workspace_status(
    connector_id: int,
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Whether this connection is enabled in this workspace, for the UI's switch."""
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_READ.value,
        "You don't have permission to view connectors in this workspace",
    )
    connector = await session.get(SearchSourceConnector, connector_id)
    if connector is None:
        raise HTTPException(status_code=404, detail="Connector not found")
    enabled = await is_enabled_in_workspace(session, connector, workspace_id)
    row = await get_enablement_row(session, connector_id, workspace_id)
    return {
        "connector_id": connector_id,
        "workspace_id": workspace_id,
        "enabled": enabled,
        "has_override": row is not None,
    }


@router.post(
    "/search-source-connectors/{connector_id}/index", response_model=dict[str, Any]
)
async def index_connector_content(
    connector_id: int,
    workspace_id: int = Query(
        ..., description="ID of the workspace to store indexed content"
    ),
    start_date: str = Query(
        None,
        description="Start date for indexing (YYYY-MM-DD format). If not provided, uses last_indexed_at or defaults to 365 days ago",
    ),
    end_date: str = Query(
        None,
        description="End date for indexing (YYYY-MM-DD format). If not provided, uses today's date.",
    ),
    drive_items: GoogleDriveIndexRequest | None = Body(
        None,
        description="[Google Drive only] Structured request with folders and files to index",
    ),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """Index content from a KB connector to a workspace."""
    try:
        # Get the connector first
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.id == connector_id
            )
        )
        connector = result.scalars().first()

        if not connector:
            raise HTTPException(status_code=404, detail="Connector not found")

        # Ensure the connector is actually enabled in the requested workspace
        # (MCP: belongs to it, unchanged; every other type: its owner is a
        # member here and it hasn't been switched off — see
        # app/services/connector_enablement.py). Without this, the permission
        # check below would authorize against the caller-supplied workspace_id
        # while the connector is someone else's or disabled here, allowing
        # cross-tenant indexing of a foreign connector (and use of its stored
        # credentials). Returning 404 (rather than 403) on a mismatch also
        # avoids disclosing the existence of connectors in other workspaces.
        if not await is_enabled_in_workspace(session, connector, workspace_id):
            raise HTTPException(status_code=404, detail="Connector not found")

        # Check if user has permission to update connectors (indexing is an
        # update operation), against the *requested* workspace — the
        # connector itself no longer has a single "own" workspace for
        # anything but MCP.
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.CONNECTORS_UPDATE.value,
            "You don't have permission to index content in this workspace",
        )

        # Handle different connector types
        response_message = ""
        indexing_started = True
        # Use UTC for consistency with last_indexed_at storage
        today_str = datetime.now(UTC).strftime("%Y-%m-%d")

        # Determine the actual date range to use
        if start_date is None:
            # Use last_indexed_at or default to 365 days ago
            if connector.last_indexed_at:
                # Convert last_indexed_at to timezone-naive for comparison (like calculate_date_range does)
                last_indexed_naive = (
                    connector.last_indexed_at.replace(tzinfo=None)
                    if connector.last_indexed_at.tzinfo
                    else connector.last_indexed_at
                )
                # Use UTC for "today" to match how last_indexed_at is stored
                today_utc = datetime.now(UTC).replace(tzinfo=None).date()
                last_indexed_date = last_indexed_naive.date()

                if last_indexed_date == today_utc:
                    # If last indexed today, go back 1 day to ensure we don't miss anything
                    indexing_from = (today_utc - timedelta(days=1)).strftime("%Y-%m-%d")
                else:
                    indexing_from = last_indexed_naive.strftime("%Y-%m-%d")
            else:
                indexing_from = (
                    datetime.now(UTC).replace(tzinfo=None) - timedelta(days=365)
                ).strftime("%Y-%m-%d")
        else:
            indexing_from = start_date

        indexing_to = end_date if end_date else today_str

        if connector.connector_type == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR:
            from app.tasks.celery_tasks.connector_tasks import (
                index_google_drive_files_task,
            )

            if drive_items and drive_items.has_items():
                logger.info(
                    f"Triggering Google Drive indexing for connector {connector_id} into workspace {workspace_id}, "
                    f"folders: {len(drive_items.folders)}, files: {len(drive_items.files)}"
                )
                items_dict = drive_items.model_dump()
            else:
                # Quick Index / periodic sync: this workspace's folder
                # selection if it set one, else the connector's account
                # default (same merge as schedule_checker_task.py).
                config = connector.config or {}
                enablement_row = await get_enablement_row(
                    session, connector.id, workspace_id
                )
                workspace_config = (
                    enablement_row.indexing_config or {}
                    if enablement_row is not None
                    else {}
                )
                selected_folders = workspace_config.get(
                    "selected_folders"
                ) or config.get("selected_folders", [])
                selected_files = workspace_config.get("selected_files") or config.get(
                    "selected_files", []
                )
                if not selected_folders and not selected_files:
                    raise HTTPException(
                        status_code=400,
                        detail="Google Drive indexing requires folders or files to be configured. "
                        "Please select folders/files to index.",
                    )
                indexing_options = workspace_config.get(
                    "indexing_options"
                ) or config.get(
                    "indexing_options",
                    {
                        "max_files_per_folder": 100,
                        "incremental_sync": True,
                        "include_subfolders": True,
                    },
                )
                items_dict = {
                    "folders": selected_folders,
                    "files": selected_files,
                    "indexing_options": indexing_options,
                }
                logger.info(
                    f"Triggering Google Drive indexing for connector {connector_id} into workspace {workspace_id} "
                    f"using existing config"
                )

            index_google_drive_files_task.delay(
                connector_id,
                workspace_id,
                str(user.id),
                items_dict,
            )
            response_message = "Google Drive indexing started in the background."

        else:
            raise HTTPException(
                status_code=400,
                detail=f"Indexing not supported for connector type: {connector.connector_type}",
            )

        return {
            "message": response_message,
            "indexing_started": indexing_started,
            "connector_id": connector_id,
            "workspace_id": workspace_id,
            "indexing_from": indexing_from,
            "indexing_to": indexing_to,
        }
    except HTTPException:
        raise
    except Exception as e:
        logger.error(
            f"Failed to initiate indexing for connector {connector_id}: {e}",
            exc_info=True,
        )
        raise HTTPException(
            status_code=500, detail=f"Failed to initiate indexing: {e!s}"
        ) from e


async def _update_connector_timestamp_by_id(session: AsyncSession, connector_id: int):
    """
    Update the last_indexed_at timestamp for a connector by its ID.
    Internal helper function for routes.

    Args:
        session: Database session
        connector_id: ID of the connector to update
    """
    try:
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.id == connector_id
            )
        )
        connector = result.scalars().first()

        if connector:
            connector.last_indexed_at = datetime.now(
                UTC
            )  # Use UTC for timezone consistency
            await session.commit()
            logger.info(f"Updated last_indexed_at for connector {connector_id}")
    except Exception as e:
        logger.error(
            f"Failed to update last_indexed_at for connector {connector_id}: {e!s}"
        )
        await session.rollback()


_AUTH_ERROR_PATTERNS = (
    "authentication failed",
    "auth_expired",
    "token has been expired or revoked",
    "invalid_grant",
)


def _is_auth_error(error_message: str) -> bool:
    """Check if an error message indicates an OAuth token expiry failure."""
    if not error_message:
        return False
    lower = error_message.lower()
    return any(pattern in lower for pattern in _AUTH_ERROR_PATTERNS)


async def _persist_auth_expired(session: AsyncSession, connector_id: int) -> None:
    """Flag a connector as auth_expired so the frontend shows a re-auth prompt."""
    from sqlalchemy.orm.attributes import flag_modified

    tracing.add_event(
        "connector.auth.expired",
        {
            "error.category": "auth_failed",
        },
    )
    try:
        result = await session.execute(
            select(SearchSourceConnector).where(
                SearchSourceConnector.id == connector_id
            )
        )
        connector = result.scalar_one_or_none()
        if connector and not connector.config.get("auth_expired"):
            connector.config = {**connector.config, "auth_expired": True}
            flag_modified(connector, "config")
            await session.commit()
            logger.info(f"Marked connector {connector_id} as auth_expired")
    except Exception:
        logger.warning(
            f"Failed to persist auth_expired for connector {connector_id}",
            exc_info=True,
        )


async def run_google_drive_indexing(
    session: AsyncSession,
    connector_id: int,
    workspace_id: int,
    user_id: str,
    items_dict: dict,  # Dictionary with 'folders', 'files', and 'indexing_options'
):
    """Runs the Google Drive indexing task for folders and files with notifications."""
    from uuid import UUID

    notification = None
    try:
        from app.tasks.connector_indexers.google_drive_indexer import (
            index_google_drive_files,
            index_google_drive_selected_files,
        )

        # Parse the structured data
        items = GoogleDriveIndexRequest(**items_dict)
        indexing_options = items.indexing_options
        total_indexed = 0
        total_skipped = 0
        errors = []

        # Get connector info for notification
        connector_result = await session.execute(
            select(SearchSourceConnector).where(
                SearchSourceConnector.id == connector_id
            )
        )
        connector = connector_result.scalar_one_or_none()

        if connector:
            # Create notification when indexing starts
            notification = await NotificationService.connector_indexing.notify_google_drive_indexing_started(
                session=session,
                user_id=UUID(user_id),
                connector_id=connector_id,
                connector_name=connector.name,
                connector_type=connector.connector_type.value,
                workspace_id=workspace_id,
                folder_count=len(items.folders),
                file_count=len(items.files),
                folder_names=items.get_folder_names() if items.folders else None,
                file_names=items.get_file_names() if items.files else None,
            )

        # Update notification to fetching stage
        if notification:
            await NotificationService.connector_indexing.notify_indexing_progress(
                session=session,
                notification=notification,
                indexed_count=0,
                stage="fetching",
            )

        total_unsupported = 0

        # Index each folder with indexing options
        for folder in items.folders:
            try:
                (
                    indexed_count,
                    skipped_count,
                    error_message,
                    unsupported_count,
                ) = await index_google_drive_files(
                    session,
                    connector_id,
                    workspace_id,
                    user_id,
                    folder_id=folder.id,
                    folder_name=folder.name,
                    use_delta_sync=indexing_options.incremental_sync,
                    update_last_indexed=False,
                    max_files=indexing_options.max_files_per_folder,
                    include_subfolders=indexing_options.include_subfolders,
                )
                total_skipped += skipped_count
                total_unsupported += unsupported_count
                if error_message:
                    errors.append(f"Folder '{folder.name}': {error_message}")
                else:
                    total_indexed += indexed_count
            except Exception as e:
                errors.append(f"Folder '{folder.name}': {e!s}")
                logger.error(
                    f"Error indexing folder {folder.name} ({folder.id}): {e}",
                    exc_info=True,
                )

        # Index all selected files together via the parallel pipeline
        if items.files:
            try:
                file_tuples = [(f.id, f.name) for f in items.files]
                (
                    indexed_count,
                    _skipped,
                    file_errors,
                ) = await index_google_drive_selected_files(
                    session,
                    connector_id,
                    workspace_id,
                    user_id,
                    files=file_tuples,
                )
                total_indexed += indexed_count
                errors.extend(file_errors)
            except Exception as e:
                errors.append(f"File batch indexing: {e!s}")
                logger.error(
                    f"Error batch indexing files: {e}",
                    exc_info=True,
                )

        # Prepare error message for notification
        error_message = None
        if errors:
            error_message = "; ".join(errors)
            logger.error(
                f"Google Drive indexing completed with errors for connector {connector_id}: {error_message}"
            )
            if _is_auth_error(error_message):
                await _persist_auth_expired(session, connector_id)
                error_message = (
                    "Google Drive authentication expired. Please re-authenticate."
                )
        else:
            # Update notification to storing stage
            if notification:
                await session.refresh(notification)
                await NotificationService.connector_indexing.notify_indexing_progress(
                    session=session,
                    notification=notification,
                    indexed_count=total_indexed,
                    stage="storing",
                )

            logger.info(
                f"Google Drive indexing successful for connector {connector_id}. Indexed {total_indexed} documents from {len(items.folders)} folder(s) and {len(items.files)} file(s)."
            )
            # Update the last indexed timestamp only on full success
            await _update_connector_timestamp_by_id(session, connector_id)
            await session.commit()  # Commit timestamp update

        # Update notification on completion
        if notification:
            # Refresh notification to reload attributes that may have been expired by earlier commits
            await session.refresh(notification)
            await NotificationService.connector_indexing.notify_indexing_completed(
                session=session,
                notification=notification,
                indexed_count=total_indexed,
                error_message=error_message,
                skipped_count=total_skipped,
                unsupported_count=total_unsupported,
            )

    except Exception as e:
        logger.error(
            f"Critical error in run_google_drive_indexing for connector {connector_id}: {e}",
            exc_info=True,
        )

        # Update notification on exception
        if notification:
            try:
                # Refresh notification to ensure it's not stale after any rollback
                await session.refresh(notification)
                await NotificationService.connector_indexing.notify_indexing_completed(
                    session=session,
                    notification=notification,
                    indexed_count=0,
                    error_message=str(e),
                )
            except Exception as notif_error:
                logger.error(f"Failed to update notification: {notif_error!s}")


@router.post("/connectors/mcp", response_model=MCPConnectorRead, status_code=201)
async def create_mcp_connector(
    connector_data: MCPConnectorCreate,
    workspace_id: int = Query(..., description="Workspace ID"),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Create a new MCP (Model Context Protocol) connector.

    MCP connectors allow users to connect to MCP servers (like in Cursor).
    Tools are auto-discovered from the server - no manual configuration needed.

    Args:
        connector_data: MCP server configuration (https url, headers)
        workspace_id: ID of the workspace to attach the connector to
        session: Database session
        user: Current authenticated user

    Returns:
        Created MCP connector with server configuration

    Raises:
        HTTPException: If workspace not found or permission denied
    """
    try:
        # Check user has permission to create connectors
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.CONNECTORS_CREATE.value,
            "You don't have permission to create connectors in this workspace",
        )
        _require_interactive_session(auth)
        origin = origin_for_new_server(auth.user)
        server_config = await _validated_mcp_server_config(
            session, connector_data.server_config, None, origin
        )

        # Ensure unique name across MCP connectors in this workspace
        unique_name = await ensure_unique_connector_name(
            session, connector_data.name, workspace_id, user.id
        )

        # Create the connector with single server config
        db_connector = SearchSourceConnector(
            name=unique_name,
            connector_type=SearchSourceConnectorType.MCP_CONNECTOR,
            is_indexable=False,  # MCP connectors are not indexable
            config={"server_config": server_config, ORIGIN_KEY: origin},
            periodic_indexing_enabled=False,
            indexing_frequency_minutes=None,
            workspace_id=workspace_id,
            user_id=user.id,
        )

        session.add(db_connector)
        await session.commit()
        await session.refresh(db_connector)

        logger.info(
            f"Created MCP connector {db_connector.id} "
            f"for user {user.id} in workspace {workspace_id}"
        )

        from app.agents.chat.multi_agent_chat.shared.tools.mcp.cache import (
            refresh_mcp_tools_cache_for_connector,
        )

        refresh_mcp_tools_cache_for_connector(db_connector.id, workspace_id)

        connector_read = SearchSourceConnectorRead.model_validate(db_connector)
        return MCPConnectorRead.from_connector(connector_read)

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to create MCP connector: {e!s}", exc_info=True)
        await session.rollback()
        raise HTTPException(
            status_code=500, detail="Failed to create MCP connector"
        ) from e


@router.get("/connectors/mcp", response_model=list[MCPConnectorRead])
async def list_mcp_connectors(
    workspace_id: int = Query(..., description="Workspace ID"),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    List all MCP connectors for a workspace.

    Args:
        workspace_id: ID of the workspace
        session: Database session
        user: Current authenticated user

    Returns:
        List of MCP connectors with their tool configurations
    """
    try:
        # Check user has permission to read connectors
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.CONNECTORS_READ.value,
            "You don't have permission to view connectors in this workspace",
        )

        # Fetch MCP connectors
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.MCP_CONNECTOR,
                SearchSourceConnector.workspace_id == workspace_id,
            )
        )

        connectors = result.scalars().all()
        return [
            MCPConnectorRead.from_connector(SearchSourceConnectorRead.model_validate(c))
            for c in connectors
        ]

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to list MCP connectors: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to list MCP connectors: {e!s}"
        ) from e


@router.get("/connectors/mcp/{connector_id}", response_model=MCPConnectorRead)
async def get_mcp_connector(
    connector_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Get a specific MCP connector by ID.

    Args:
        connector_id: ID of the connector
        session: Database session
        user: Current authenticated user

    Returns:
        MCP connector with tool configurations
    """
    try:
        # Fetch connector
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.id == connector_id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.MCP_CONNECTOR,
            )
        )
        connector = result.scalars().first()

        if not connector:
            raise HTTPException(status_code=404, detail="MCP connector not found")

        # Check user has permission to read connectors
        await check_permission(
            session,
            auth,
            connector.workspace_id,
            Permission.CONNECTORS_READ.value,
            "You don't have permission to view this connector",
        )

        connector_read = SearchSourceConnectorRead.model_validate(connector)
        return MCPConnectorRead.from_connector(connector_read)

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to get MCP connector: {e!s}", exc_info=True)
        raise HTTPException(
            status_code=500, detail=f"Failed to get MCP connector: {e!s}"
        ) from e


@router.put("/connectors/mcp/{connector_id}", response_model=MCPConnectorRead)
async def update_mcp_connector(
    connector_id: int,
    connector_update: MCPConnectorUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Update an MCP connector.

    Args:
        connector_id: ID of the connector to update
        connector_update: Updated connector data
        session: Database session
        user: Current authenticated user

    Returns:
        Updated MCP connector
    """
    try:
        # Fetch connector
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.id == connector_id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.MCP_CONNECTOR,
            )
        )
        connector = result.scalars().first()

        if not connector:
            raise HTTPException(status_code=404, detail="MCP connector not found")

        # Check user has permission to update connectors
        await check_permission(
            session,
            auth,
            connector.workspace_id,
            Permission.CONNECTORS_UPDATE.value,
            "You don't have permission to update this connector",
        )
        _require_mcp_editor(auth, connector)

        # Update fields
        if connector_update.name is not None:
            connector.name = connector_update.name

        if connector_update.server_config is not None:
            config = dict(connector.config or {})
            config["server_config"] = await _validated_mcp_server_config(
                session,
                connector_update.server_config,
                (connector.config or {}).get("server_config"),
                origin_of(connector.config),
            )
            config.pop("auth_expired", None)
            connector.config = config

        connector.updated_at = datetime.now(UTC)

        await session.commit()
        await session.refresh(connector)

        logger.info(f"Updated MCP connector {connector_id}")

        from app.agents.chat.multi_agent_chat.shared.tools.mcp.cache import (
            refresh_mcp_tools_cache_for_connector,
        )

        refresh_mcp_tools_cache_for_connector(connector.id, connector.workspace_id)

        connector_read = SearchSourceConnectorRead.model_validate(connector)
        return MCPConnectorRead.from_connector(connector_read)

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to update MCP connector: {e!s}", exc_info=True)
        await session.rollback()
        raise HTTPException(
            status_code=500, detail="Failed to update MCP connector"
        ) from e


@router.delete("/connectors/mcp/{connector_id}", status_code=204)
async def delete_mcp_connector(
    connector_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Delete an MCP connector.

    Args:
        connector_id: ID of the connector to delete
        session: Database session
        user: Current authenticated user
    """
    try:
        # Fetch connector
        result = await session.execute(
            select(SearchSourceConnector).filter(
                SearchSourceConnector.id == connector_id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.MCP_CONNECTOR,
            )
        )
        connector = result.scalars().first()

        if not connector:
            raise HTTPException(status_code=404, detail="MCP connector not found")

        await _require_mcp_remover(session, auth, connector)

        workspace_id = connector.workspace_id
        await session.delete(connector)
        await session.commit()

        from app.agents.chat.multi_agent_chat.shared.tools.mcp.tool import (
            invalidate_mcp_tools_cache,
        )

        invalidate_mcp_tools_cache(workspace_id)

        logger.info(f"Deleted MCP connector {connector_id}")

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to delete MCP connector: {e!s}", exc_info=True)
        await session.rollback()
        raise HTTPException(
            status_code=500, detail=f"Failed to delete MCP connector: {e!s}"
        ) from e


@router.post("/connectors/mcp/test")
async def test_mcp_server_connection(
    server_config: dict = Body(...),
    workspace_id: int = Query(..., description="Workspace ID"),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """Test a remote MCP server before saving it and list its tools.

    Signed-in sessions only (no access tokens), with permission to add
    connectors in the workspace, and rate limited. The server must resolve to
    public addresses, and a site server's host (an administrator's) must be on
    the allow-list. Every failure gets the same generic message: the reply
    never carries error text, so this cannot be used to probe what the
    backend can reach.
    """
    from app.agents.chat.multi_agent_chat.shared.tools.mcp.client import (
        GENERIC_FAILURE,
        test_mcp_http_connection,
    )

    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    _check_mcp_test_rate(auth.user.id)

    raw = dict(server_config)
    connector_id = raw.pop("connector_id", None)
    try:
        cfg = MCPServerConfig.model_validate(raw)
    except ValidationError as exc:
        raise HTTPException(status_code=422, detail=_validation_detail(exc)) from exc

    stored: dict[str, Any] | None = None
    origin = origin_for_new_server(auth.user)
    if connector_id is not None:
        # Testing a saved server whose form holds masked keys.
        if not isinstance(connector_id, int):
            raise HTTPException(status_code=422, detail="connector_id must be a number")
        existing = await session.get(SearchSourceConnector, connector_id)
        if (
            existing is not None
            and existing.workspace_id == workspace_id
            and existing.connector_type == SearchSourceConnectorType.MCP_CONNECTOR
        ):
            _require_mcp_editor(auth, existing)
            stored = (existing.config or {}).get("server_config")
            origin = origin_of(existing.config)

    try:
        data = restore_masked(cfg.model_dump(), stored)
    except MaskedSecretError:
        return {"status": "error", "message": GENERIC_FAILURE, "tools": []}

    try:
        hosts = await reachable_hosts_for_save(session, origin, data["url"])
    except MCPDestinationRefusedError:
        return {"status": "error", "message": GENERIC_FAILURE, "tools": []}
    return await test_mcp_http_connection(data["url"], data.get("headers"), hosts)


# =============================================================================
# MCP Tool Trust (Allow-List) Routes
# =============================================================================


class MCPTrustToolRequest(BaseModel):
    tool_name: str


async def _ensure_mcp_connector_for_user(
    session: AsyncSession, *, user_id, connector_id: int
) -> int:
    """Verify ``connector_id`` is an MCP-backed connector owned by ``user_id``.

    The trust-list feature is intentionally MCP-only; native connectors
    do not have a "trust this tool" UI.
    The JSONB ``has_key("server_config")`` filter is the same MCP marker
    used elsewhere in this module.

    Returns the connector's ``workspace_id`` (needed downstream for
    MCP tool cache invalidation). Raises ``HTTPException(404)`` when the
    connector does not exist, is not owned by the user, or is not
    MCP-backed.
    """
    from sqlalchemy import cast
    from sqlalchemy.dialects.postgresql import JSONB as PG_JSONB

    result = await session.execute(
        select(SearchSourceConnector.workspace_id).where(
            SearchSourceConnector.id == connector_id,
            SearchSourceConnector.user_id == user_id,
            cast(SearchSourceConnector.config, PG_JSONB).has_key("server_config"),
        )
    )
    workspace_id = result.scalar_one_or_none()
    if workspace_id is None:
        raise HTTPException(status_code=404, detail="MCP connector not found")
    return workspace_id


@router.post("/connectors/mcp/{connector_id}/trust-tool")
async def trust_mcp_tool(
    connector_id: int,
    body: MCPTrustToolRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """Add a tool to the MCP connector's trusted (always-allow) list.

    Once trusted, the tool executes without HITL approval on subsequent
    calls. The storage primitive is the JSON list under
    ``config.trusted_tools``.
    """
    from app.agents.chat.multi_agent_chat.shared.tools.mcp.tool import (
        invalidate_mcp_tools_cache,
    )
    from app.services.user_tool_allowlist import add_user_trust

    if always_asks(body.tool_name):
        raise HTTPException(
            status_code=400,
            detail="Tools that open web addresses always ask before running",
        )

    try:
        workspace_id = await _ensure_mcp_connector_for_user(
            session, user_id=user.id, connector_id=connector_id
        )
        trusted = await add_user_trust(
            session,
            user_id=user.id,
            connector_id=connector_id,
            tool_name=body.tool_name,
        )
        await session.commit()
        invalidate_mcp_tools_cache(workspace_id)
        return {"status": "ok", "trusted_tools": trusted}

    except HTTPException:
        raise
    except LookupError as e:
        raise HTTPException(status_code=404, detail="MCP connector not found") from e
    except Exception as e:
        logger.error(f"Failed to trust MCP tool: {e!s}", exc_info=True)
        await session.rollback()
        raise HTTPException(
            status_code=500, detail=f"Failed to trust tool: {e!s}"
        ) from e


@router.post("/connectors/mcp/{connector_id}/untrust-tool")
async def untrust_mcp_tool(
    connector_id: int,
    body: MCPTrustToolRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """Remove a tool from the MCP connector's trusted list.

    The tool will require HITL approval again on subsequent calls.
    """
    from app.agents.chat.multi_agent_chat.shared.tools.mcp.tool import (
        invalidate_mcp_tools_cache,
    )
    from app.services.user_tool_allowlist import remove_user_trust

    try:
        workspace_id = await _ensure_mcp_connector_for_user(
            session, user_id=user.id, connector_id=connector_id
        )
        trusted = await remove_user_trust(
            session,
            user_id=user.id,
            connector_id=connector_id,
            tool_name=body.tool_name,
        )
        await session.commit()
        invalidate_mcp_tools_cache(workspace_id)
        return {"status": "ok", "trusted_tools": trusted}

    except HTTPException:
        raise
    except LookupError as e:
        raise HTTPException(status_code=404, detail="MCP connector not found") from e
    except Exception as e:
        logger.error(f"Failed to untrust MCP tool: {e!s}", exc_info=True)
        await session.rollback()
        raise HTTPException(
            status_code=500, detail=f"Failed to untrust tool: {e!s}"
        ) from e
