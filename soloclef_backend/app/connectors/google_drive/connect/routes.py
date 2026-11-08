"""HTTP door of connecting Google Drive: status, consent, Google's callback, Picker, disconnect.

A Drive connection belongs to the person who made it: only they get its
Picker session, and connecting again reuses their row. The callback path is
the redirect URI registered in Google Cloud Console, so it must not move.

A connect is bound to the browser that started it (``connect_attempt.py``):
the start sets an HttpOnly cookie with the attempt, and the callback saves
nothing without it. A connect the phone app started (``app/app_handoff``)
saves nothing at the callback either: the grant is held, encrypted
(``held_grant.py``), under a one-time completion (5 minutes) and the sheet
goes to ``soloclef://connectors/done?purpose=drive_connect&status=confirm
&completion=…``; the app saves it with its bearer token and its PKCE
verifier (``POST /api/v1/app/handoff/complete``, then ``save_connection``).
"""

from __future__ import annotations

import logging
import time
from datetime import UTC, datetime, timedelta
from urllib.parse import urlencode
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from fastapi.responses import RedirectResponse
from pydantic import BaseModel, Field
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm.attributes import flag_modified

from app.app_handoff.codes import (
    DRIVE_CONNECT,
    HandoffCodes,
    HandoffGrant,
    completion_store,
)
from app.app_handoff.returns import CONFIRM, app_return, configured_return
from app.auth.context import AuthContext
from app.config import config
from app.db import (
    Permission,
    SearchSourceConnector,
    SearchSourceConnectorType,
    get_async_session,
)
from app.services.connector_enablement import get_enablement_row
from app.users import require_session_context
from app.utils.connector_naming import generate_unique_connector_name
from app.utils.rbac import check_permission, check_workspace_access

from ..change_tracker import get_start_page_token
from ..client import GoogleDriveClient
from ..credentials import get_valid_credentials
from . import connect_attempt, held_grant
from .account_lookup import account_email
from .connect_attempt import AttemptRefusedError, ConnectAttempt
from .connection_record import ensure_enabled, owned_drive_connector, users_connection
from .oauth_flow import consent_url, credentials_config, exchange_code, granted_scopes
from .readiness import missing_settings, picker_app_id
from .revocation import revoke_stored_grant
from .schemas import (
    ConnectStarted,
    Disconnected,
    GoogleDriveConnectionRead,
    PickerSession,
)
from .scopes import DRIVE_FILE_SCOPE
from .stored_grant import replace_grant

logger = logging.getLogger(__name__)

router = APIRouter()

_WORKSPACE = "/workspaces/{workspace_id}/google-drive"
CALLBACK_PATH = "/auth/google/drive/connector/callback"
RESYNC_MINUTES = 1440
#: The callback's answer when it is not the connect this browser started (no
#: cookie, another browser, a replay, or more than 10 minutes).
EXPIRED = "google_drive_connect_expired"


class ConnectRequest(BaseModel):
    #: Where in the app to land after Google (a path like ``/dashboard/3/notebooks/7``).
    return_path: str | None = Field(default=None, max_length=512)


_safe_return_path = connect_attempt.safe_return_path


def _secret() -> str:
    if not config.SECRET_KEY:
        raise HTTPException(status_code=500, detail="SECRET_KEY is not set")
    return config.SECRET_KEY


def _not_configured(missing: list[str]) -> HTTPException:
    return HTTPException(
        status_code=503,
        detail={
            "code": "google_drive_not_configured",
            "message": "Google Drive is not set up on this server",
            "missing": missing,
        },
    )


def _reconnect() -> HTTPException:
    return HTTPException(
        status_code=400,
        detail={
            "code": "google_drive_reconnect",
            "message": "Google no longer accepts this connection; connect again",
        },
    )


def connection_view(
    connector: SearchSourceConnector | None,
) -> GoogleDriveConnectionRead:
    missing = missing_settings()
    view = GoogleDriveConnectionRead(configured=not missing, missing=missing)
    if connector is None:
        return view
    cfg = connector.config or {}
    view.connector_id = connector.id
    view.account_email = cfg.get("account_email") or None
    view.needs_reconnect = bool(cfg.get("auth_expired")) or not cfg.get("refresh_token")
    return view


@router.get(_WORKSPACE + "/connection", response_model=GoogleDriveConnectionRead)
async def read_connection(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """This person's Drive connection here, and whether the server can make one."""
    await check_workspace_access(session, auth, workspace_id)
    return connection_view(await users_connection(session, auth.user.id, workspace_id))


def ensure_ready() -> None:
    """503 with the missing settings' names when this server cannot connect Drive."""
    missing = missing_settings()
    if missing:
        raise _not_configured(missing)


def begin_connect(
    response: Response,
    *,
    workspace_id: int,
    user_id: UUID,
    return_path: str | None = None,
    app: bool = False,
    app_challenge: str | None = None,
) -> str:
    """Seal the attempt in this browser's cookie (on ``response``); Google's consent URL.

    The browser that receives ``response`` is the only one whose callback is
    accepted. For the phone app (``app``) that is whichever browser opened
    the hand-off page, so its callback only holds the grant for the app to
    confirm with the verifier behind ``app_challenge``.
    """
    attempt = ConnectAttempt.start(
        user_id=user_id,
        workspace_id=workspace_id,
        return_path=return_path,
        app=app,
        app_challenge=app_challenge,
    )
    redirect_uri = config.GOOGLE_DRIVE_REDIRECT_URI
    response.set_cookie(
        connect_attempt.COOKIE_NAME,
        attempt.seal(_secret()),
        max_age=connect_attempt.LIFETIME_SECONDS,
        path=connect_attempt.cookie_path(redirect_uri),
        httponly=True,
        secure=connect_attempt.cookie_secure(redirect_uri),
        # Lax: sent on Google's top-level redirect back to this site.
        samesite="lax",
    )
    return consent_url(attempt.state, attempt.code_verifier)


@router.post(_WORKSPACE + "/connect", response_model=ConnectStarted)
async def start_connect(
    workspace_id: int,
    response: Response,
    body: ConnectRequest | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """Google's consent URL for connecting (or reconnecting) this person's Drive."""
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    ensure_ready()
    return_path = _safe_return_path(body.return_path if body else None)
    return ConnectStarted(
        auth_url=begin_connect(
            response,
            workspace_id=workspace_id,
            user_id=auth.user.id,
            return_path=return_path,
        )
    )


@router.get("/auth/google/drive/connector/add", response_model=ConnectStarted)
async def start_connect_from_catalog(
    space_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """The connectors panel's Connect: Google returns to its callback page."""
    return await start_connect(space_id, response, None, session, auth)


@router.get("/auth/google/drive/connector/reauth", response_model=ConnectStarted)
async def start_reconnect(
    space_id: int,
    connector_id: int,
    response: Response,
    return_url: str | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """Reconnect an expired connection; the grant replaces the one in the same row."""
    connector = await owned_drive_connector(session, auth.user.id, connector_id)
    if connector is None:
        raise HTTPException(status_code=404, detail="Google Drive connection not found")
    return await start_connect(
        space_id, response, ConnectRequest(return_path=return_url), session, auth
    )


def _back_to_app(
    space_id: int | None,
    return_path: str | None,
    *,
    app: bool = False,
    completion: str | None = None,
    **params: str,
):
    """Where the callback ends, the attempt's cookie cleared on the way."""
    if app:
        # The phone app's browser sheet: its own address, with the outcome the
        # website gets as ?google_drive= (connected, or the refusal's code),
        # or ``confirm`` and the completion the app saves the grant with.
        response = RedirectResponse(
            url=app_return(
                DRIVE_CONNECT,
                params.get("outcome") or "failed",
                configured_return(),
                completion=completion,
            ),
            status_code=303,
        )
        response.headers["Cache-Control"] = "no-store"
        response.headers["Referrer-Policy"] = "no-referrer"
    else:
        if return_path:
            sep = "&" if "?" in return_path else "?"
            target = f"{return_path}{sep}{urlencode({'google_drive': params.get('outcome', '')})}"
        elif space_id is not None:
            query = urlencode({k: v for k, v in params.items() if k != "outcome"})
            target = f"/dashboard/{space_id}/connectors/callback?{query}"
        else:
            target = "/dashboard"
        response = RedirectResponse(url=f"{config.NEXT_FRONTEND_URL}{target}")
    response.delete_cookie(
        connect_attempt.COOKIE_NAME,
        path=connect_attempt.cookie_path(config.GOOGLE_DRIVE_REDIRECT_URI),
    )
    return response


def _refused(
    space_id: int | None, return_path: str | None, error: str, *, app: bool = False
):
    return _back_to_app(
        space_id,
        return_path,
        app=app,
        outcome=error,
        error=error,
        connector="google-drive-connector",
    )


async def _save_grant(
    session: AsyncSession,
    *,
    user_id: UUID,
    workspace_id: int,
    grant: dict,
    email: str | None,
) -> SearchSourceConnector:
    connector = await users_connection(session, user_id, workspace_id)
    if connector is not None:
        previous = dict(connector.config or {})
        connector.config = replace_grant(previous, grant, email)
        flag_modified(connector, "config")
        # Reconnecting (e.g. after "needs_reconnect") never resets whichever
        # workspaces already have it switched on or configured for syncing —
        # only clears an explicit disable for *this* workspace, same as a
        # brand-new connect does below.
        await ensure_enabled(session, connector, workspace_id)
        await session.flush()
        if previous.get("refresh_token") and previous.get(
            "refresh_token"
        ) != connector.config.get("refresh_token"):
            await revoke_stored_grant(previous)
        return connector
    connector = SearchSourceConnector(
        name=await generate_unique_connector_name(
            session,
            SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
            workspace_id,
            user_id,
            email,
        ),
        connector_type=SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        config=replace_grant(None, grant, email),
        workspace_id=None,
        user_id=user_id,
        is_indexable=True,
    )
    session.add(connector)
    await session.flush()
    # New connection: switch on syncing in the workspace connected from,
    # same default (daily resync) the connector itself used to carry.
    # Account-wide; doesn't sync into any other workspace automatically.
    enablement = await get_enablement_row(session, connector.id, workspace_id)
    if enablement is None:
        from app.db import SearchSourceConnectorEnablement

        enablement = SearchSourceConnectorEnablement(
            connector_id=connector.id,
            workspace_id=workspace_id,
            enabled_by_user_id=user_id,
        )
        session.add(enablement)
    enablement.is_indexable = True
    # Picked files are re-read daily when they changed in Drive.
    enablement.periodic_indexing_enabled = True
    enablement.indexing_frequency_minutes = RESYNC_MINUTES
    enablement.next_scheduled_at = datetime.now(UTC) + timedelta(minutes=RESYNC_MINUTES)
    await session.flush()
    return connector


async def _remember_start_token(session: AsyncSession, connector) -> None:
    """Where Drive's change feed starts, for later delta syncs (best effort)."""
    try:
        token, error = await get_start_page_token(
            GoogleDriveClient(session, connector.id)
        )
    except Exception:
        logger.warning("Reading Drive's start page token failed", exc_info=True)
        return
    if token and not error:
        connector.config = {**connector.config, "start_page_token": token}
        flag_modified(connector, "config")
        await session.commit()


async def save_connection(
    session: AsyncSession,
    *,
    user_id: UUID,
    workspace_id: int,
    grant: dict,
    email: str | None,
) -> SearchSourceConnector:
    """Save a Drive grant on the person's connection: the website's callback,
    or the grant held for the phone app once the app confirmed it."""
    connector = await _save_grant(
        session, user_id=user_id, workspace_id=workspace_id, grant=grant, email=email
    )
    await session.commit()
    await session.refresh(connector)
    await _remember_start_token(session, connector)
    return connector


@router.get(CALLBACK_PATH)
async def finish_connect(
    request: Request,
    code: str | None = None,
    error: str | None = None,
    state: str | None = None,
    session: AsyncSession = Depends(get_async_session),
    completions: HandoffCodes = Depends(completion_store),
):
    """Where Google sends the person back; saves the grant and returns to the app.

    Only for the browser that started this connect (its cookie). A connect
    the phone app started saves nothing here: the grant is held for the app
    to confirm (``app_handoff.routes.complete_handoff``), since the browser
    that came back is only whichever one opened the app's hand-off link.
    """
    try:
        attempt = connect_attempt.reopen(
            request.cookies.get(connect_attempt.COOKIE_NAME), state, _secret()
        )
    except AttemptRefusedError as refused:
        logger.info("Google Drive connect refused: %s", refused)
        return _refused(connect_attempt.workspace_named_in(state), None, EXPIRED)
    space_id = attempt.workspace_id
    return_path = attempt.return_path
    app = attempt.app
    if error or not code:
        return _refused(space_id, return_path, "google_drive_oauth_denied", app=app)

    try:
        credentials = await exchange_code(code, attempt.code_verifier)
    except Exception as exc:
        logger.warning("Google code exchange failed: %s", type(exc).__name__)
        return _refused(space_id, return_path, "google_drive_oauth_failed", app=app)
    if DRIVE_FILE_SCOPE not in granted_scopes(credentials):
        return _refused(space_id, return_path, "google_drive_scope_missing", app=app)
    if not credentials.refresh_token:
        return _refused(space_id, return_path, "google_drive_no_refresh_token", app=app)

    email = await account_email(credentials)
    if app:
        return await _hold_for_the_app(
            attempt, credentials_config(credentials), email, completions
        )
    connector = await save_connection(
        session,
        user_id=UUID(attempt.user_id),
        workspace_id=space_id,
        grant=credentials_config(credentials),
        email=email,
    )
    return _back_to_app(
        space_id,
        return_path,
        outcome="connected",
        success="true",
        connector="google-drive-connector",
        connectorId=str(connector.id),
    )


async def _hold_for_the_app(
    attempt: ConnectAttempt, grant: dict, email: str | None, completions: HandoffCodes
):
    """An app attempt's grant, held (encrypted) until the app confirms it.

    Never saved here: the hand-off link may have been opened by someone else
    (a link the account's owner sent them), and their Drive must not land on
    the owner's account. An app attempt without the app's challenge (none is
    ever made, ``app_handoff.routes.open_handoff`` refuses first) fails.
    """
    space_id = attempt.workspace_id
    if not attempt.app_challenge:
        logger.warning("Google Drive connect for the app carried no challenge")
        return _refused(space_id, None, "google_drive_oauth_failed", app=True)
    try:
        completion = await completions.issue(
            HandoffGrant(
                purpose=DRIVE_CONNECT,
                user_id=attempt.user_id,
                workspace_id=space_id,
                issued_at=time.time(),
                challenge=attempt.app_challenge,
                sealed=held_grant.seal(grant, email, secret_key=_secret()),
            )
        )
    except Exception:
        logger.exception("Holding a Google Drive grant for the app failed")
        return _refused(space_id, None, "google_drive_oauth_failed", app=True)
    logger.info(
        "Google Drive grant held for the app of user %s, to be confirmed",
        attempt.user_id,
    )
    return _back_to_app(
        space_id, None, app=True, completion=completion, outcome=CONFIRM
    )


async def _mark_expired(
    session: AsyncSession, connector: SearchSourceConnector
) -> None:
    connector.config = {**(connector.config or {}), "auth_expired": True}
    flag_modified(connector, "config")
    await session.commit()


@router.get(
    "/connectors/{connector_id}/google-drive/picker-session",
    response_model=PickerSession,
)
async def picker_session(
    connector_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """A fresh access token for the Google Picker, for the connection's owner only.

    Under ``drive.file`` the token reaches only files this app was given, so
    the Picker is what widens it: each file picked is granted to the app.
    """
    connector = await owned_drive_connector(session, auth.user.id, connector_id)
    if connector is None:
        raise HTTPException(status_code=404, detail="Google Drive connection not found")
    # owned_drive_connector already confirms this is the caller's own
    # connection (account-level, no single workspace to check access on).
    missing = missing_settings()
    if missing:
        raise _not_configured(missing)
    try:
        credentials = await get_valid_credentials(session, connector_id)
    except Exception as exc:
        text = str(exc).lower()
        if "re-authenticate" in text or "invalid_grant" in text:
            await _mark_expired(session, connector)
            raise _reconnect() from exc
        logger.warning("Refreshing a Drive token failed: %s", type(exc).__name__)
        raise HTTPException(status_code=502, detail="Google did not answer") from exc
    return PickerSession(
        access_token=credentials.token,
        api_key=config.GOOGLE_PICKER_API_KEY,
        app_id=picker_app_id(),
    )


@router.delete(
    "/connectors/{connector_id}/google-drive/connection", response_model=Disconnected
)
async def disconnect(
    connector_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """Revoke the grant at Google and remove the connection, in every workspace.

    Imported files stay: they are ordinary workspace documents, still in
    their notebooks. Account-level connection: only its owner may disconnect
    it (see ``custom/ACCOUNT_CONNECTIONS.md`` — "only the connector's owner
    can edit, delete, or switch it on/off").
    """
    connector = await owned_drive_connector(session, auth.user.id, connector_id)
    if connector is None:
        raise HTTPException(status_code=404, detail="Google Drive connection not found")
    revoked = await revoke_stored_grant(connector.config)
    if connector.periodic_indexing_enabled:
        from app.utils.periodic_scheduler import delete_periodic_schedule

        delete_periodic_schedule(connector.id)
    await session.delete(connector)
    await session.commit()
    return Disconnected(revoked=revoked)
