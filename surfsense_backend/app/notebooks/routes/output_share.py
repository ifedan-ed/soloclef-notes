"""Share a finished Studio output through a read-only public link."""

from __future__ import annotations

from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.public_sharing.expiry import PublicLinkCreate
from app.public_sharing.output_links import create_output_snapshot
from app.schemas.new_chat import PublicChatSnapshotCreateResponse
from app.users import get_auth_context

from ..access import authorized_notebook

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/studio/outputs/{output_id}"


@router.post(_BASE + "/public-link", response_model=PublicChatSnapshotCreateResponse)
async def share_output_publicly(
    workspace_id: int,
    notebook_id: int,
    output_id: int,
    body: PublicLinkCreate | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    return await create_output_snapshot(
        session,
        auth,
        notebook,
        output_id,
        (body or PublicLinkCreate()).expires_in_days,
    )
