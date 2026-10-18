"""HTTP door of connecting Nextcloud: status, start a login, poll it, disconnect.

Everything acts for the signed-in person in a workspace they belong to; a
connection is theirs alone. The browser gets a random flow id and the login
URL to open; the poll token and the app password stay on the server.
"""

from __future__ import annotations

import logging

from fastapi import APIRouter, Depends, HTTPException, Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import (
    Permission,
    SearchSourceConnector,
    SearchSourceConnectorType,
    get_async_session,
)
from app.users import require_session_context
from app.utils.rbac import check_permission, check_workspace_access

from ..client import NextcloudAccount, NextcloudError
from ..connector_credentials import (
    InvalidNextcloudSettingsError,
    account_from_config,
    stored_config,
)
from ..server_address import (
    NextcloudNotConfiguredError,
    configured_server_url,
    require_server_url,
    server_label,
)
from .attempt_limits import AttemptLimits
from .connection_record import save_connection, users_connection
from .login_api import GrantedLogin, NextcloudLoginError
from .pending_logins import LIFETIME_SECONDS, PendingLogin, PendingLogins, new_flow_id
from .revocation import revoke_stored_password
from .schemas import (
    Disconnected,
    LoginProgress,
    LoginStarted,
    NextcloudAccountRead,
    NextcloudConnectionRead,
)
from .wiring import (
    LoginApiFactory,
    WebdavClientFactory,
    get_attempt_limits,
    get_login_api_factory,
    get_pending_logins,
    get_webdav_client_factory,
)

logger = logging.getLogger(__name__)

router = APIRouter()

_WORKSPACE = "/workspaces/{workspace_id}/nextcloud"


def connection_view(connector: SearchSourceConnector | None) -> NextcloudConnectionRead:
    server = configured_server_url()
    view = NextcloudConnectionRead(
        configured=server is not None,
        server=server_label(server) if server else None,
    )
    if connector is None:
        return view
    cfg = connector.config or {}
    view.connector_id = connector.id
    view.account = NextcloudAccountRead(
        user_id=str(cfg.get("username") or ""),
        display_name=str(cfg.get("display_name") or cfg.get("username") or ""),
    )
    try:
        account_from_config(cfg)
    except (InvalidNextcloudSettingsError, ValueError):
        view.needs_reconnect = True
    return view


def _not_configured() -> HTTPException:
    return HTTPException(
        status_code=503,
        detail={
            "code": "nextcloud_not_configured",
            "message": "Nextcloud is not set up on this server",
        },
    )


def _too_many() -> HTTPException:
    return HTTPException(
        status_code=429,
        detail={
            "code": "nextcloud_rate_limited",
            "message": "Too many Nextcloud sign-in attempts; wait a minute",
        },
    )


def _flow_gone() -> HTTPException:
    # The same answer for "expired" and "not yours", so ids cannot be probed.
    return HTTPException(
        status_code=404,
        detail={
            "code": "nextcloud_login_expired",
            "message": "This Nextcloud sign-in expired; start again",
        },
    )


@router.get(_WORKSPACE + "/connection", response_model=NextcloudConnectionRead)
async def read_connection(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """This person's Nextcloud connection here, and which server it would use."""
    await check_workspace_access(session, auth, workspace_id)
    return connection_view(await users_connection(session, auth.user.id, workspace_id))


@router.post(_WORKSPACE + "/login", response_model=LoginStarted)
async def start_login(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
    pending: PendingLogins = Depends(get_pending_logins),
    limits: AttemptLimits = Depends(get_attempt_limits),
    api_for: LoginApiFactory = Depends(get_login_api_factory),
):
    """Ask Nextcloud for a login URL; the backend keeps the poll token."""
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    try:
        server = require_server_url()
    except NextcloudNotConfiguredError as exc:
        raise _not_configured() from exc
    if not await limits.may_start(auth.user.id):
        raise _too_many()
    try:
        started = await api_for(server).start()
    except NextcloudLoginError as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc
    flow_id = new_flow_id()
    await pending.add(
        flow_id,
        PendingLogin(
            user_id=str(auth.user.id),
            workspace_id=workspace_id,
            poll_endpoint=started.poll_endpoint,
            poll_token=started.poll_token,
            login_url=started.login_url,
        ),
    )
    return LoginStarted(
        flow_id=flow_id, login_url=started.login_url, expires_in=LIFETIME_SECONDS
    )


async def _checked_account(
    server: str,
    granted: GrantedLogin,
    api_for: LoginApiFactory,
    client_for: WebdavClientFactory,
) -> tuple[NextcloudAccount, str]:
    """The new login, proven by listing the person's files, and their display name."""
    api = api_for(server)
    identity = await api.identity(granted.login_name, granted.app_password)
    account = NextcloudAccount(
        base_url=server,
        username=identity.user_id,
        app_password=granted.app_password,
        login_name=granted.login_name,
    )
    await client_for(account).stat("/")
    return account, identity.display_name


@router.post(_WORKSPACE + "/login/{flow_id}/poll", response_model=LoginProgress)
async def poll_login(
    workspace_id: int,
    flow_id: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
    pending: PendingLogins = Depends(get_pending_logins),
    limits: AttemptLimits = Depends(get_attempt_limits),
    api_for: LoginApiFactory = Depends(get_login_api_factory),
    client_for: WebdavClientFactory = Depends(get_webdav_client_factory),
):
    """Ask Nextcloud once whether the person granted access; save the login if so."""
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    if not await limits.may_poll(auth.user.id):
        raise _too_many()
    flow = await pending.get(flow_id)
    if flow is None or not flow.belongs_to(auth.user.id, workspace_id):
        raise _flow_gone()
    try:
        server = require_server_url()
    except NextcloudNotConfiguredError as exc:
        raise _not_configured() from exc
    api = api_for(server)
    try:
        granted = await api.poll(flow.poll_endpoint, flow.poll_token)
    except NextcloudLoginError as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc
    if granted is None:
        return LoginProgress(status="pending")

    # Nextcloud hands the password over once: from here the flow is spent.
    await pending.remove(flow_id)
    try:
        account, display_name = await _checked_account(
            server, granted, api_for, client_for
        )
    except (NextcloudLoginError, NextcloudError) as exc:
        await api.revoke(granted.login_name, granted.app_password)
        raise HTTPException(
            status_code=502,
            detail={"code": "nextcloud_login_unusable", "message": str(exc)},
        ) from exc

    connector, previous = await save_connection(
        session,
        user_id=auth.user.id,
        workspace_id=workspace_id,
        config=stored_config(
            server,
            account.username,
            account.app_password,
            login_name=account.login_name,
            display_name=display_name,
        ),
    )
    await session.commit()
    logger.info(
        "Nextcloud connected for user %s in workspace %s (connector %s)",
        auth.user.id,
        workspace_id,
        connector.id,
    )
    if previous is not None:
        # The replaced app password is of no further use; retire it.
        await revoke_stored_password(previous, api_for=api_for)
    return LoginProgress(status="connected", connection=connection_view(connector))


@router.delete(_WORKSPACE + "/login/{flow_id}", status_code=204)
async def cancel_login(
    workspace_id: int,
    flow_id: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
    pending: PendingLogins = Depends(get_pending_logins),
):
    """Stop polling a login the person abandoned. Nextcloud drops it after 20 minutes."""
    await check_workspace_access(session, auth, workspace_id)
    flow = await pending.get(flow_id)
    if flow is not None and flow.belongs_to(auth.user.id, workspace_id):
        await pending.remove(flow_id)
    return Response(status_code=204)


@router.delete(
    "/connectors/{connector_id}/nextcloud/connection", response_model=Disconnected
)
async def disconnect(
    connector_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
    api_for: LoginApiFactory = Depends(get_login_api_factory),
):
    """Revoke the app password in Nextcloud and remove the connection, everywhere.

    Imported files stay: they are ordinary workspace documents. Account-level
    connection: only its owner may disconnect it (``custom/ACCOUNT_CONNECTIONS.md``).
    """
    connector = await session.scalar(
        select(SearchSourceConnector).where(
            SearchSourceConnector.id == connector_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR,
        )
    )
    if connector is None:
        raise HTTPException(status_code=404, detail="Nextcloud connection not found")
    if connector.user_id != auth.user.id:
        raise HTTPException(
            status_code=403,
            detail="Only this connection's owner can delete it",
        )
    revoked = await revoke_stored_password(connector.config, api_for=api_for)
    await session.delete(connector)
    await session.commit()
    return Disconnected(revoked=revoked)
