"""HTTP door of the Email (IMAP) connector: manage accounts, test connections.

Every route acts for the caller only — accounts are looked up by
``(workspace_id, user_id=auth.user.id)``, so one user's mailboxes are never
reachable through another user's token, even within the same workspace.
Actual mail search/read happens through the chat tools
(``subagents/connectors/email_imap``), not these routes.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context, require_session_context
from app.utils.rbac import check_permission, check_workspace_access

from .credentials import InvalidEmailAccountError
from .schemas import (
    EmailAccountCreate,
    EmailAccountRead,
    RawTestConnectionRequest,
    TestConnectionResult,
)
from .service import (
    EmailAccountNotFoundError,
    add_account,
    delete_account,
    list_accounts as _list_accounts,
    test_connection as _test_connection,
    test_raw_connection as _test_raw_connection,
)

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/connectors/email-imap/accounts"


async def _checked(session: AsyncSession, auth: AuthContext, workspace_id: int) -> None:
    await check_workspace_access(session, auth, workspace_id)


@router.get(_BASE, response_model=list[EmailAccountRead])
async def list_email_accounts(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await _checked(session, auth, workspace_id)
    accounts = await _list_accounts(
        session, workspace_id=workspace_id, user_id=auth.user.id
    )
    return accounts


@router.post(_BASE, response_model=EmailAccountRead, status_code=201)
async def add_email_account(
    workspace_id: int,
    body: EmailAccountCreate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await _checked(session, auth, workspace_id)
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    try:
        return await add_account(
            session,
            workspace_id=workspace_id,
            user_id=auth.user.id,
            label=body.label,
            host=body.host,
            port=body.port,
            use_tls=body.use_tls,
            username=body.username,
            app_password=body.app_password,
        )
    except InvalidEmailAccountError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


@router.delete(_BASE + "/{account_id}", status_code=204)
async def delete_email_account(
    workspace_id: int,
    account_id: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await _checked(session, auth, workspace_id)
    try:
        await delete_account(
            session,
            workspace_id=workspace_id,
            user_id=auth.user.id,
            account_id=account_id,
        )
    except EmailAccountNotFoundError as exc:
        raise HTTPException(status_code=404, detail="Account not found") from exc


@router.post("/connectors/email-imap/test", response_model=TestConnectionResult)
async def test_unsaved_email_account(
    body: RawTestConnectionRequest,
    workspace_id: int = Query(..., description="Workspace ID"),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    """Test connection settings from the add-account form, before saving.

    Nothing is persisted here — this never touches the database. Signed-in
    sessions only, same as the MCP pre-save test endpoint.
    """
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.CONNECTORS_CREATE.value,
        "You don't have permission to add connectors in this workspace",
    )
    return await _test_raw_connection(
        host=body.host,
        port=body.port,
        use_tls=body.use_tls,
        username=body.username,
        app_password=body.app_password,
    )


@router.post(_BASE + "/{account_id}/test", response_model=TestConnectionResult)
async def test_email_account(
    workspace_id: int,
    account_id: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await _checked(session, auth, workspace_id)
    try:
        result = await _test_connection(
            session,
            workspace_id=workspace_id,
            user_id=auth.user.id,
            account_id=account_id,
        )
    except EmailAccountNotFoundError as exc:
        raise HTTPException(status_code=404, detail="Account not found") from exc
    except InvalidEmailAccountError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return result
