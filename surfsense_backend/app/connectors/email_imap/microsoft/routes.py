"""HTTP door of connecting a Microsoft mailbox: availability, consent, Microsoft's callback.

The callback path is the redirect URI registered in Microsoft Entra
(``{BACKEND_URL}/api/v1`` + ``settings.CALLBACK_PATH``), so it must not move.
Every way a callback can end lands back in the app with
``?microsoft_mail=<outcome>`` (``outcomes.py``), never on a bare JSON error.
Removing a mailbox, testing it, and reading mail go through the ordinary
Email (IMAP) routes and tools.

A connect the phone app started (its browser hand-off, ``app/app_handoff``)
ends differently: the callback saves nothing, holds the mailbox encrypted
(``held_mailbox.py``) under a one-time completion (5 minutes), and sends the
sheet to ``soloclef://connectors/done?purpose=microsoft_mail&status=confirm
&completion=…``; the mailbox is saved when the app spends that completion with
its bearer token and its PKCE verifier (``POST /api/v1/app/handoff/complete``).
"""

from __future__ import annotations

import logging
import time
from urllib.parse import urlencode
from uuid import UUID

import httpx
from fastapi import APIRouter, Depends, HTTPException, Request, Response
from fastapi.responses import RedirectResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app.app_handoff.codes import (
    MICROSOFT_MAIL,
    HandoffCodes,
    HandoffGrant,
    completion_store,
)
from app.app_handoff.returns import CONFIRM, app_return, configured_return
from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context, require_session_context
from app.utils.rbac import check_permission, check_workspace_access

from . import connect_attempt, held_mailbox, outcomes
from .connect_attempt import ConnectAttempt
from .consent import consent_url
from .mailbox_identity import UnknownMailboxError, mailbox_from_id_token
from .mailbox_store import MicrosoftMailboxStore
from .outcomes import ConnectRefusedError
from .schemas import (
    MicrosoftConnectRequest,
    MicrosoftConnectStarted,
    MicrosoftMailAvailability,
)
from .settings import CALLBACK_PATH, MicrosoftMailSettings, current_settings
from .token_endpoint import GrantRevokedError, TokenEndpointError, redeem_code

logger = logging.getLogger(__name__)

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/connectors/email-imap/microsoft"
# Microsoft's error codes for "this organization lets only admins consent".
_ADMIN_APPROVAL_CODES = ("AADSTS65001", "AADSTS90094", "AADSTS90095")


def get_microsoft_mail_settings() -> MicrosoftMailSettings:
    return current_settings()


def get_token_transport() -> httpx.AsyncBaseTransport | None:
    """How to reach Microsoft's token endpoint; ``None`` is the real network."""
    return None


def get_mailbox_store(
    session: AsyncSession = Depends(get_async_session),
) -> MicrosoftMailboxStore:
    return MicrosoftMailboxStore(session)


def _connectors_page(workspace_id: int | None) -> str:
    if workspace_id is None:
        return "/dashboard"
    return f"/dashboard/{workspace_id}/workspace-settings/connectors"


@router.get(_BASE, response_model=MicrosoftMailAvailability)
async def read_availability(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    settings: MicrosoftMailSettings = Depends(get_microsoft_mail_settings),
):
    """Whether this server can connect Microsoft mailboxes (only that, no setting names)."""
    await check_workspace_access(session, auth, workspace_id)
    return MicrosoftMailAvailability(configured=settings.configured)


@router.post(_BASE + "/connect", response_model=MicrosoftConnectStarted)
async def start_connect(
    workspace_id: int,
    response: Response,
    body: MicrosoftConnectRequest | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
    settings: MicrosoftMailSettings = Depends(get_microsoft_mail_settings),
    store: MicrosoftMailboxStore = Depends(get_mailbox_store),
):
    """Microsoft's consent page for connecting (or reconnecting) a mailbox."""
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    ensure_configured(settings)
    body = body or MicrosoftConnectRequest()
    return MicrosoftConnectStarted(
        auth_url=await begin_connect(
            response,
            user_id=auth.user.id,
            workspace_id=workspace_id,
            settings=settings,
            store=store,
            account_id=body.account_id,
            return_path=body.return_path,
        )
    )


def ensure_configured(settings: MicrosoftMailSettings) -> None:
    """503 when this server has no Microsoft app set up."""
    if not settings.configured:
        raise HTTPException(
            status_code=503,
            detail={
                "code": "microsoft_mail_not_configured",
                "message": "Microsoft accounts aren't set up on this server",
            },
        )


async def begin_connect(
    response: Response,
    *,
    user_id,
    workspace_id: int,
    settings: MicrosoftMailSettings,
    store: MicrosoftMailboxStore,
    account_id: str | None = None,
    return_path: str | None = None,
    app: bool = False,
    app_challenge: str | None = None,
) -> str:
    """Seal the attempt in this browser's cookie (on ``response``); Microsoft's consent URL.

    The browser that receives ``response`` is the only one whose callback is
    accepted. For the phone app (``app``) that is whichever browser opened
    the hand-off page (app_handoff/routes.py), so its callback only holds the
    mailbox for the app to confirm with the verifier behind ``app_challenge``.
    """
    login_hint = await store.address_of(user_id, account_id) if account_id else None
    attempt = ConnectAttempt.start(
        user_id=user_id,
        workspace_id=workspace_id,
        return_path=return_path,
        app=app,
        app_challenge=app_challenge,
    )
    response.set_cookie(
        connect_attempt.COOKIE_NAME,
        attempt.seal(settings.secret_key),
        max_age=connect_attempt.LIFETIME_SECONDS,
        path=settings.cookie_path,
        httponly=True,
        secure=settings.cookie_secure,
        # Lax: sent on Microsoft's top-level redirect back to this site.
        samesite="lax",
    )
    return consent_url(settings, attempt, login_hint=login_hint)


def _refusal_from_microsoft(error: str, description: str) -> ConnectRefusedError:
    # Anyone can put anything in these query values: log a short repr only.
    said = f"Microsoft: {error[:64]!r}"
    if error == "consent_required" or any(
        code in description for code in _ADMIN_APPROVAL_CODES
    ):
        return ConnectRefusedError(outcomes.ADMIN_APPROVAL, said)
    if error == "access_denied":
        return ConnectRefusedError(outcomes.DENIED, "declined on Microsoft's page")
    return ConnectRefusedError(outcomes.FAILED, said)


@router.get(CALLBACK_PATH)
async def finish_connect(
    request: Request,
    settings: MicrosoftMailSettings = Depends(get_microsoft_mail_settings),
    transport: httpx.AsyncBaseTransport | None = Depends(get_token_transport),
    store: MicrosoftMailboxStore = Depends(get_mailbox_store),
    completions: HandoffCodes = Depends(completion_store),
):
    """Where Microsoft sends the person back: save the mailbox, return to the app.

    A connect the phone app started saves nothing here: the mailbox is held
    for the app to confirm (``complete_handoff``), since the browser that
    came back is only whichever one opened the app's hand-off link.
    """
    params = request.query_params
    state = params.get("state")
    attempt: ConnectAttempt | None = None

    def back(outcome: str, completion: str | None = None) -> RedirectResponse:
        if attempt is not None and attempt.app:
            # The phone app's browser sheet: back to the app's own address.
            redirect = RedirectResponse(
                app_return(
                    MICROSOFT_MAIL,
                    outcome,
                    configured_return(),
                    completion=completion,
                ),
                status_code=303,
            )
            redirect.headers["Cache-Control"] = "no-store"
            redirect.headers["Referrer-Policy"] = "no-referrer"
            redirect.delete_cookie(
                connect_attempt.COOKIE_NAME, path=settings.cookie_path
            )
            return redirect
        if attempt is not None:
            page = attempt.return_path or _connectors_page(attempt.workspace_id)
        else:
            page = _connectors_page(connect_attempt.workspace_named_in(state))
        sep = "&" if "?" in page else "?"
        target = f"{settings.frontend_url}{page}{sep}{urlencode({outcomes.QUERY_PARAM: outcome})}"
        redirect = RedirectResponse(target, status_code=303)
        redirect.delete_cookie(connect_attempt.COOKIE_NAME, path=settings.cookie_path)
        return redirect

    if not settings.configured:
        return back(outcomes.NOT_CONFIGURED)
    try:
        attempt = connect_attempt.reopen(
            request.cookies.get(connect_attempt.COOKIE_NAME),
            state,
            settings.secret_key,
        )
        if params.get("error"):
            raise _refusal_from_microsoft(
                params.get("error", ""), params.get("error_description", "")
            )
        code = params.get("code")
        if not code:
            raise ConnectRefusedError(outcomes.FAILED, "callback carries no code")
        try:
            grant = await redeem_code(
                settings, code, attempt.code_verifier, transport=transport
            )
        except GrantRevokedError as exc:
            if str(exc) == "consent_required":
                raise ConnectRefusedError(outcomes.ADMIN_APPROVAL, str(exc)) from exc
            raise ConnectRefusedError(outcomes.FAILED, f"code refused: {exc}") from exc
        except TokenEndpointError as exc:
            raise ConnectRefusedError(outcomes.FAILED, f"code exchange: {exc}") from exc
        if not grant.grants_imap:
            raise ConnectRefusedError(outcomes.SCOPE_MISSING, "IMAP access not granted")
        if not grant.refresh_token:
            raise ConnectRefusedError(outcomes.NO_REFRESH_TOKEN, "no refresh token")
        try:
            mailbox = mailbox_from_id_token(grant.id_token, settings.client_id)
        except UnknownMailboxError as exc:
            raise ConnectRefusedError(outcomes.NO_ADDRESS, str(exc)) from exc
    except ConnectRefusedError as refused:
        logger.info(
            "Microsoft mailbox connect refused (%s): %s", refused.outcome, refused
        )
        return back(refused.outcome)

    if attempt.app:
        return await _hold_for_the_app(
            attempt, mailbox, grant, settings, completions, back
        )

    try:
        await store.save(
            UUID(attempt.user_id), mailbox, grant, secret_key=settings.secret_key
        )
    except Exception:
        logger.exception("Saving a connected Microsoft mailbox failed")
        return back(outcomes.FAILED)
    logger.info("Microsoft mailbox connected for user %s", attempt.user_id)
    return back(outcomes.CONNECTED)


async def _hold_for_the_app(attempt, mailbox, grant, settings, completions, back):
    """An app attempt's mailbox, held (encrypted) until the app confirms it.

    Never saved here: the hand-off link may have been opened by someone else
    (a link the account's owner sent them), and their mailbox must not land
    on the owner's account. An app attempt without the app's challenge (none
    is ever made, ``app_handoff.routes.open_handoff`` refuses first) fails.
    """
    if not attempt.app_challenge:
        logger.warning("Microsoft mailbox connect for the app carried no challenge")
        return back(outcomes.FAILED)
    try:
        completion = await completions.issue(
            HandoffGrant(
                purpose=MICROSOFT_MAIL,
                user_id=attempt.user_id,
                workspace_id=attempt.workspace_id,
                issued_at=time.time(),
                challenge=attempt.app_challenge,
                sealed=held_mailbox.seal(
                    mailbox, grant, secret_key=settings.secret_key
                ),
            )
        )
    except Exception:
        logger.exception("Holding a Microsoft mailbox for the app failed")
        return back(outcomes.FAILED)
    logger.info(
        "Microsoft mailbox held for the app of user %s, to be confirmed",
        attempt.user_id,
    )
    return back(CONFIRM, completion)
