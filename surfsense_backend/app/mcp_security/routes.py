"""Admin endpoints for the MCP host allowlist, plus who may add MCP servers."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Query
from pydantic import BaseModel, Field
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.billing.admin_access import require_billing_admin
from app.db import Permission, get_async_session, has_permission
from app.users import get_auth_context
from app.utils.rbac import get_user_permissions

from .allowlist import is_mcp_admin, load_allowed_hosts, save_allowed_hosts

router = APIRouter(tags=["mcp-security"])


class AllowedHostsBody(BaseModel):
    hosts: list[str] = Field(default_factory=list, max_length=100)


@router.get("/connectors/mcp/policy")
async def mcp_policy(
    workspace_id: int | None = Query(None),
    auth: AuthContext = Depends(get_auth_context),
    session: AsyncSession = Depends(get_async_session),
):
    """What the connector form needs to know.

    ``can_add``: a signed-in person who may add connectors in the workspace
    (Owner or Editor). ``allow_list_applies``: an administrator's server is a
    site server and must be on the allow-list; anyone else's is their own and
    only needs to be a public https address (``server_origin``).
    """
    can_add = auth.is_session
    if can_add and workspace_id is not None:
        permissions = await get_user_permissions(session, auth.user.id, workspace_id)
        can_add = has_permission(permissions, Permission.CONNECTORS_CREATE.value)
    return {
        "can_add": can_add,
        "allow_list_applies": auth.is_session and is_mcp_admin(auth.user),
    }


@router.get("/admin/mcp/allowed-hosts")
async def get_allowed_hosts(
    _auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    return {"hosts": sorted(await load_allowed_hosts(session))}


@router.put("/admin/mcp/allowed-hosts")
async def put_allowed_hosts(
    body: AllowedHostsBody,
    auth: AuthContext = Depends(require_billing_admin),
    session: AsyncSession = Depends(get_async_session),
):
    try:
        hosts = await save_allowed_hosts(session, body.hosts, actor=auth.user.id)
    except ValueError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    await session.commit()
    return {"hosts": hosts}
