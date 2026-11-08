"""A Studio output's viewer frame: its prompt and sources."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Response
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context

from ..access import authorized_notebook
from ..studio import output_view as service

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/studio/outputs/{output_id}"


@router.get(_BASE + "/view", response_model=service.OutputViewRead)
async def get_output_view(
    workspace_id: int,
    notebook_id: int,
    output_id: int,
    response: Response,
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
    try:
        view = await service.output_view(session, notebook, output_id)
    except service.OutputNotFoundError:
        raise HTTPException(status_code=404, detail="Output not found") from None
    response.headers["Cache-Control"] = "private, no-store"
    return view
