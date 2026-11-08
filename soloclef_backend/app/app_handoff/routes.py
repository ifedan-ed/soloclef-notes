"""HTTP doors of the phone app's browser hand-off (B2): the Drive Picker (B6),
Drive connect and Microsoft mail (B10).

See this package's ``__init__`` for the flow. Limits (``custom/README.md``,
"The phone app's browser hand-off"): a code lives 60 s and opens one page; a
Picker page's ticket lives 10 minutes and makes one import; a Microsoft or
Drive completion lives 5 minutes and saves one mailbox or Drive; 20
hand-offs, 30 pages and 20 completions a minute from one address.

The page takes no credentials, so whoever opens a code's link runs its flow
in their own browser. For Microsoft mail and Google Drive that would link
the opener's mailbox or Drive to the account that made the code (a link sent
to someone else, or an account's own page minting a fresh code for every
visitor). So those purposes (``CONFIRMED_PURPOSES``) need the app's PKCE
``code_challenge`` with the code, their callback saves nothing and sends the
sheet back with a one-time ``completion``, and the mailbox or Drive is saved
only by ``POST /app/handoff/complete {completion, code_verifier}`` with the
bearer token of the same account and the verifier behind that challenge.
The person who opened someone else's link holds a completion they cannot
spend, and the link's maker never sees it. An app build that sends no
challenge is told to update (``HANDOFF_CHALLENGE_REQUIRED``).

The permission check happens when the code is asked for, with the app's
token, and is the website's own for that flow (adding documents to the
notebook for the Picker, adding connectors for Drive connect and Microsoft
mail); the page only spends the code (the Picker's page checks again, as the
person, before it shows the Picker). A connector sign-in starts inside the
browser that opened the page, as the website's own Connect buttons do: the
attempt's signed cookie must be set in the browser that comes back to the
callback (each provider's ``connect_attempt.py``). When
the provider is done, its callback sends the sheet to the app's address
(``returns.py``). No token, and no spendable code, ever appears in a URL a
provider can see: the code is spent before the redirect, and the page tells
the browser to send no Referer.

A code that is unknown, spent or older than 60 s opens nothing: the Picker
page (whose address names it, ``?for=drive_picker``) says so with a 410
page; any other sends the sheet back to ``soloclef://connectors/done`` with
``status=expired``.
"""

from __future__ import annotations

import logging
import time
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from pydantic import BaseModel, Field
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.auth_oidc.app_sign_in import verifier_matches
from app.connectors.email_imap.microsoft import held_mailbox, routes as microsoft_routes
from app.connectors.email_imap.microsoft.mailbox_store import MicrosoftMailboxStore
from app.connectors.email_imap.microsoft.settings import MicrosoftMailSettings
from app.connectors.google_drive.connect import (
    held_grant as drive_held_grant,
    routes as drive_connect,
)
from app.connectors.google_drive.connect.connection_record import users_connection
from app.connectors.google_drive.connect.readiness import missing_settings
from app.connectors.google_drive.notebook_import import routes as drive_import
from app.connectors.google_drive.notebook_import.schemas import (
    MAX_PICKED_FILES,
    DriveImportRequest,
    PickedDriveFile,
)
from app.db import Permission, SearchSourceConnector, User, get_async_session
from app.rate_limiter import limiter
from app.users import require_session_context
from app.utils.rbac import check_permission, check_workspace_access

from .codes import (
    CODE_LIFETIME_SECONDS,
    CONFIRMED_PURPOSES,
    DRIVE_CONNECT,
    DRIVE_PICKER,
    MICROSOFT_MAIL,
    HandoffCodes,
    HandoffGrant,
    code_store,
    completion_store,
    ticket_store,
)
from .picker_page import (
    PickerPage,
    new_nonce,
    render_message_page,
    render_picker_page,
    security_headers,
)
from .purposes import UNSUPPORTED, app_return, is_supported
from .returns import (
    EXPIRED as RETURN_EXPIRED,
    NOT_CONFIGURED,
    app_return as connector_return,
    configured_return,
)

logger = logging.getLogger(__name__)

router = APIRouter()

_START_LIMIT = "20/minute"
_PAGE_LIMIT = "30/minute"
_IMPORT_LIMIT = "30/minute"
_COMPLETE_LIMIT = "20/minute"

PAGE_PATH = "/api/v1/app/handoff/{code}"
IMPORT_PATH = "/api/v1/app/handoff/drive-picker/import"
EXPIRED = "HANDOFF_EXPIRED"
#: A confirmed purpose asked for without the app's PKCE challenge (an app
#: build from before completions): the app shows the message.
CHALLENGE_REQUIRED = "HANDOFF_CHALLENGE_REQUIRED"
#: A completion spent with a verifier that is not its challenge's.
WRONG_VERIFIER = "HANDOFF_WRONG_VERIFIER"
#: A completion spent by another account than the one that asked for the code.
WRONG_ACCOUNT = "HANDOFF_WRONG_ACCOUNT"

# The headers every connector hand-off answers with: never cached, and no
# Referer from it to the provider's page.
_PAGE_HEADERS = {"Cache-Control": "no-store", "Referrer-Policy": "no-referrer"}


class HandoffRequest(BaseModel):
    #: drive_picker, drive_connect or microsoft_mail (anything else: HANDOFF_PURPOSE_UNSUPPORTED).
    purpose: str = Field(min_length=1, max_length=40)
    workspace_id: int
    #: The Drive Picker: the notebook its files are added to; none: the library only.
    notebook_id: int | None = None
    #: Microsoft mail: the mailbox being reconnected, so its address is preselected.
    account_id: str | None = Field(default=None, max_length=64)
    #: Microsoft mail (required there; ``CONFIRMED_PURPOSES``): the S256 PKCE
    #: challenge of a verifier the app keeps, to confirm the mailbox with.
    code_challenge: str | None = Field(default=None, pattern=r"^[A-Za-z0-9_-]{43}$")


class HandoffStarted(BaseModel):
    #: Opens the hand-off page once, within ``expires_in`` seconds.
    code: str
    expires_in: int
    #: The page's path on this server (the app puts its server's address before it).
    path: str


class PickerImportRequest(BaseModel):
    #: The Picker page's ticket (it stands for the person, the workspace and the notebook).
    ticket: str = Field(min_length=20, max_length=200)
    files: list[PickedDriveFile] = Field(min_length=1, max_length=MAX_PICKED_FILES)


class PickerImported(BaseModel):
    document_ids: list[int]
    queued: int
    unsupported: list[str]


class HandoffCompletion(BaseModel):
    #: The ``completion`` the sheet came back with (``status=confirm``).
    completion: str = Field(min_length=20, max_length=200)
    #: The verifier behind the ``code_challenge`` the hand-off was asked with.
    code_verifier: str = Field(min_length=43, max_length=128)


class HandoffCompleted(BaseModel):
    purpose: str
    #: "connected": the website's own outcome word (``microsoft_mail=connected``).
    status: str


def get_handoff_codes() -> HandoffCodes:
    """The code store (Redis); a test overrides it, or sets ``codes._codes``."""
    return code_store()


def _refusal(status: int, code: str, message: str) -> HTTPException:
    return HTTPException(status_code=status, detail={"code": code, "message": message})


async def drive_picker_connection(
    session: AsyncSession, auth: AuthContext, workspace_id: int, notebook_id: int | None
) -> SearchSourceConnector:
    """This person's usable Drive connection, once they may import into the notebook."""
    await check_workspace_access(session, auth, workspace_id)
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.DOCUMENTS_CREATE.value,
        "You don't have permission to create documents in this workspace",
    )
    if notebook_id is not None:
        from app.notebooks.access import authorized_notebook

        await authorized_notebook(
            session,
            auth,
            workspace_id,
            notebook_id,
            Permission.CHATS_UPDATE,
            Permission.DOCUMENTS_READ,
        )
    missing = missing_settings()
    if missing:
        raise _refusal(
            503,
            "google_drive_not_configured",
            "Google Drive is not set up on this server",
        )
    connector = await users_connection(session, auth.user.id, workspace_id)
    if connector is None:
        raise _refusal(404, "google_drive_not_connected", "Connect Google Drive first")
    if drive_connect.connection_view(connector).needs_reconnect:
        raise _refusal(
            400,
            "google_drive_reconnect",
            "Google no longer accepts this connection; connect again",
        )
    return connector


@router.post("/app/handoff", response_model=HandoffStarted)
@limiter.limit(_START_LIMIT)
async def start_handoff(
    request: Request,
    body: HandoffRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
    codes: HandoffCodes = Depends(get_handoff_codes),
    microsoft: MicrosoftMailSettings = Depends(
        microsoft_routes.get_microsoft_mail_settings
    ),
):
    """A one-time code for the browser sheet (60 s, single use, one purpose), after the website's own checks."""
    if not is_supported(body.purpose):
        raise _refusal(
            400, UNSUPPORTED, "The app can't hand this off to the browser yet"
        )
    if body.purpose == DRIVE_PICKER:
        await drive_picker_connection(
            session, auth, body.workspace_id, body.notebook_id
        )
    else:
        await check_permission(
            session,
            auth,
            body.workspace_id,
            Permission.CONNECTORS_CREATE.value,
            "You don't have permission to add connectors in this workspace",
        )
        if body.purpose == DRIVE_CONNECT:
            drive_connect.ensure_ready()
        else:
            microsoft_routes.ensure_configured(microsoft)
    # After the website's checks, so a server without the provider set up
    # still says so to an app build that sends no challenge.
    if body.purpose in CONFIRMED_PURPOSES and not body.code_challenge:
        what = (
            "Google Drive" if body.purpose == DRIVE_CONNECT else "a Microsoft account"
        )
        raise _refusal(
            400,
            CHALLENGE_REQUIRED,
            f"Update the app to connect {what} from it, or connect it on the website",
        )
    code = await codes.issue(
        HandoffGrant(
            purpose=body.purpose,
            user_id=str(auth.user.id),
            workspace_id=body.workspace_id,
            issued_at=time.time(),
            notebook_id=body.notebook_id if body.purpose == DRIVE_PICKER else None,
            account_id=body.account_id if body.purpose == MICROSOFT_MAIL else None,
            challenge=body.code_challenge
            if body.purpose in CONFIRMED_PURPOSES
            else None,
        )
    )
    # The Picker's page names its purpose, so a spent code still gets the Picker's own answer.
    path = PAGE_PATH.format(code=code) + (
        f"?for={DRIVE_PICKER}" if body.purpose == DRIVE_PICKER else ""
    )
    started = HandoffStarted(code=code, expires_in=CODE_LIFETIME_SECONDS, path=path)
    return JSONResponse(started.model_dump(), headers={"Cache-Control": "no-store"})


def _page(content: str, nonce: str, status: int = 200) -> HTMLResponse:
    return HTMLResponse(content, status_code=status, headers=security_headers(nonce))


def _back(purpose: str, error: str) -> RedirectResponse:
    response = RedirectResponse(app_return(purpose, error=error), status_code=302)
    response.headers["Cache-Control"] = "no-store"
    response.headers["Referrer-Policy"] = "no-referrer"
    return response


def _to(url: str) -> RedirectResponse:
    return RedirectResponse(url, status_code=303, headers=dict(_PAGE_HEADERS))


async def _signed_in_user(session: AsyncSession, user_id: str) -> User | None:
    try:
        user = await session.get(User, UUID(user_id))
    except ValueError:
        return None
    return user if user is not None and user.is_active else None


@router.get("/app/handoff/{code}", response_class=HTMLResponse)
@limiter.limit(_PAGE_LIMIT)
async def open_handoff(
    request: Request,
    code: str,
    session: AsyncSession = Depends(get_async_session),
    codes: HandoffCodes = Depends(get_handoff_codes),
    microsoft: MicrosoftMailSettings = Depends(
        microsoft_routes.get_microsoft_mail_settings
    ),
    store: MicrosoftMailboxStore = Depends(microsoft_routes.get_mailbox_store),
):
    """The browser sheet's page: spends the code and starts the flow it is for, in this browser."""
    grant = await codes.redeem(code[:128])
    if grant is None:
        if request.query_params.get("for") == DRIVE_PICKER:
            nonce = new_nonce()
            return _page(
                render_message_page(
                    "This link has run out",
                    "A link from the app opens once, within a minute. Close this page and try again in the app.",
                    None,
                    nonce,
                ),
                nonce,
                status=410,
            )
        return _to(connector_return(None, RETURN_EXPIRED, configured_return()))
    if grant.purpose == DRIVE_PICKER:
        return await _open_drive_picker(session, grant)
    back = configured_return()
    if grant.purpose == DRIVE_CONNECT:
        if drive_connect.missing_settings():
            return _to(connector_return(DRIVE_CONNECT, NOT_CONFIGURED, back))
        if not grant.challenge:
            # Never issued without one (start_handoff); refused all the same.
            return _to(connector_return(DRIVE_CONNECT, RETURN_EXPIRED, back))
        page = Response(status_code=303, headers=dict(_PAGE_HEADERS))
        # This browser may be anyone's: the callback only holds the grant,
        # for the app to confirm with the verifier behind grant.challenge.
        page.headers["Location"] = drive_connect.begin_connect(
            page,
            workspace_id=grant.workspace_id,
            user_id=UUID(grant.user_id),
            app=True,
            app_challenge=grant.challenge,
        )
        return page
    if grant.purpose == MICROSOFT_MAIL:
        if not microsoft.configured:
            return _to(connector_return(MICROSOFT_MAIL, NOT_CONFIGURED, back))
        if not grant.challenge:
            # Never issued without one (start_handoff); refused all the same.
            return _to(connector_return(MICROSOFT_MAIL, RETURN_EXPIRED, back))
        page = Response(status_code=303, headers=dict(_PAGE_HEADERS))
        # This browser may be anyone's: the callback only holds the mailbox,
        # for the app to confirm with the verifier behind grant.challenge.
        page.headers["Location"] = await microsoft_routes.begin_connect(
            page,
            user_id=UUID(grant.user_id),
            workspace_id=grant.workspace_id,
            settings=microsoft,
            store=store,
            account_id=grant.account_id,
            app=True,
            app_challenge=grant.challenge,
        )
        return page
    logger.warning(
        "app hand-off: a code with an unknown purpose %r", grant.purpose[:32]
    )
    return _to(connector_return(None, RETURN_EXPIRED, back))


async def _open_drive_picker(session: AsyncSession, grant: HandoffGrant):
    """The Picker's page, with a ticket for its one import; or back to the app saying why not."""
    nonce = new_nonce()
    user = await _signed_in_user(session, grant.user_id)
    if user is None:
        return _back(DRIVE_PICKER, "signed_out")
    auth = AuthContext.session(user)
    try:
        connector = await drive_picker_connection(
            session, auth, grant.workspace_id, grant.notebook_id
        )
        picker = await drive_connect.picker_session(connector.id, session, auth)
    except HTTPException as exc:
        detail = exc.detail if isinstance(exc.detail, dict) else {}
        if detail.get("code") == "google_drive_reconnect":
            return _back(DRIVE_PICKER, "google_drive_reconnect")
        logger.info("Drive Picker hand-off refused: %s", exc.status_code)
        return _back(DRIVE_PICKER, "picker_failed")
    ticket = await ticket_store().issue(
        HandoffGrant(
            purpose=DRIVE_PICKER,
            user_id=grant.user_id,
            workspace_id=grant.workspace_id,
            issued_at=time.time(),
            notebook_id=grant.notebook_id,
            connector_id=connector.id,
        )
    )
    page = PickerPage(
        access_token=picker.access_token,
        api_key=picker.api_key,
        app_id=picker.app_id,
        ticket=ticket,
        import_path=IMPORT_PATH,
        done_url=app_return(DRIVE_PICKER),
    )
    return _page(render_picker_page(page, nonce), nonce)


@router.post("/app/handoff/drive-picker/import", response_model=PickerImported)
@limiter.limit(_IMPORT_LIMIT)
async def import_from_picker_page(
    request: Request,
    body: PickerImportRequest,
    session: AsyncSession = Depends(get_async_session),
    dispatch: drive_import.ImportDispatcher = Depends(
        drive_import.get_import_dispatcher
    ),
):
    """The Picker page's one import, as the web's ``…/google-drive/import`` makes it.

    No cookie or bearer: the page's ticket is the credential, and it is in
    the page's body only, so another site cannot make this request.
    """
    grant = await ticket_store().redeem(body.ticket)
    if grant is None or grant.purpose != DRIVE_PICKER or grant.connector_id is None:
        raise _refusal(410, EXPIRED, "This page has run out; pick again in the app")
    user = await _signed_in_user(session, grant.user_id)
    if user is None:
        raise _refusal(410, EXPIRED, "This page has run out; pick again in the app")
    result = await drive_import.import_picked_files(
        grant.connector_id,
        DriveImportRequest(
            workspace_id=grant.workspace_id,
            files=body.files,
            notebook_id=grant.notebook_id,
        ),
        session,
        AuthContext.session(user),
        dispatch,
    )
    imported = PickerImported(
        document_ids=result.document_ids,
        queued=result.queued,
        unsupported=result.unsupported,
    )
    return JSONResponse(imported.model_dump(), headers={"Cache-Control": "no-store"})


@router.post("/app/handoff/complete", response_model=HandoffCompleted)
@limiter.limit(_COMPLETE_LIMIT)
async def complete_handoff(
    request: Request,
    body: HandoffCompletion,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
    completions: HandoffCodes = Depends(completion_store),
    microsoft: MicrosoftMailSettings = Depends(
        microsoft_routes.get_microsoft_mail_settings
    ),
    store: MicrosoftMailboxStore = Depends(microsoft_routes.get_mailbox_store),
):
    """Save the mailbox or Drive a hand-off held, for the app that asked for it.

    The completion is spent by the first try, right or wrong. It saves only
    with the verifier behind the code's challenge, from the account that asked
    for the code, which may still add connectors in that workspace.
    """
    held = await completions.redeem(body.completion)
    if (
        held is None
        or held.purpose not in CONFIRMED_PURPOSES
        or not held.challenge
        or not held.sealed
    ):
        raise _refusal(410, EXPIRED, "This took too long; connect again")
    if not verifier_matches(body.code_verifier, held.challenge):
        logger.warning("app hand-off completion with a verifier that is not its own")
        raise _refusal(
            403, WRONG_VERIFIER, "This connection wasn't started here; connect again"
        )
    if held.user_id != str(auth.user.id):
        logger.warning(
            "app hand-off completion for user %s spent by user %s: refused",
            held.user_id,
            auth.user.id,
        )
        raise _refusal(
            403,
            WRONG_ACCOUNT,
            "This was connected through another account's link; nothing was saved",
        )
    await check_permission(
        session,
        auth,
        held.workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    if held.purpose == DRIVE_CONNECT:
        return await _save_held_drive(session, auth, held)
    microsoft_routes.ensure_configured(microsoft)
    try:
        mailbox, grant = held_mailbox.unseal(
            held.sealed, secret_key=microsoft.secret_key
        )
    except held_mailbox.HeldMailboxError:
        logger.warning("app hand-off completion: the held mailbox does not open")
        raise _refusal(410, EXPIRED, "This took too long; connect again") from None
    try:
        await store.save(auth.user.id, mailbox, grant, secret_key=microsoft.secret_key)
    except Exception:
        logger.exception("Saving a Microsoft mailbox the app confirmed failed")
        raise _refusal(
            500, "HANDOFF_SAVE_FAILED", "Couldn't save the mailbox; connect again"
        ) from None
    logger.info("Microsoft mailbox connected from the app for user %s", auth.user.id)
    done = HandoffCompleted(purpose=held.purpose, status="connected")
    return JSONResponse(done.model_dump(), headers={"Cache-Control": "no-store"})


async def _save_held_drive(
    session: AsyncSession, auth: AuthContext, held: HandoffGrant
):
    """The Drive a hand-off's callback held, saved for the app that confirmed it."""
    drive_connect.ensure_ready()
    try:
        grant, email = drive_held_grant.unseal(
            held.sealed or "", secret_key=drive_connect._secret()
        )
    except drive_held_grant.HeldGrantError:
        logger.warning("app hand-off completion: the held Drive grant does not open")
        raise _refusal(410, EXPIRED, "This took too long; connect again") from None
    try:
        await drive_connect.save_connection(
            session,
            user_id=auth.user.id,
            workspace_id=held.workspace_id,
            grant=grant,
            email=email,
        )
    except Exception:
        logger.exception("Saving a Google Drive the app confirmed failed")
        raise _refusal(
            500, "HANDOFF_SAVE_FAILED", "Couldn't save Google Drive; connect again"
        ) from None
    logger.info("Google Drive connected from the app for user %s", auth.user.id)
    done = HandoffCompleted(purpose=held.purpose, status="connected")
    return JSONResponse(done.model_dump(), headers={"Cache-Control": "no-store"})
