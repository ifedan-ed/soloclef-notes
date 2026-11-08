"""Read and change a workspace's quick-tasks model (Settings -> Models)."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import delete
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, Workspace, get_async_session
from app.users import get_auth_context
from app.utils.rbac import check_permission

from .model import WorkspaceQuickModel
from .resolution import (
    DEFAULT_QUICK_MODEL_ID,
    SAME_AS_CHAT,
    _db_quick_model,
    _global_quick_model,
    default_quick_model,
    stored_quick_model_id,
)

router = APIRouter()
_PATH = "/workspaces/{workspace_id}/quick-model"


class QuickModelRead(BaseModel):
    # None: the default; 0: the chat model; otherwise a model role id.
    model_id: int | None
    # What "default" resolves to right now; None means the chat model.
    default_model_id: int | None
    default_model_name: str = DEFAULT_QUICK_MODEL_ID


class QuickModelUpdate(BaseModel):
    model_id: int | None


async def _workspace(session: AsyncSession, workspace_id: int) -> Workspace:
    workspace = await session.get(Workspace, workspace_id)
    if workspace is None:
        raise HTTPException(status_code=404, detail="Workspace not found")
    return workspace


async def _read(session: AsyncSession, workspace: Workspace) -> QuickModelRead:
    default = await default_quick_model(session, workspace)
    return QuickModelRead(
        model_id=await stored_quick_model_id(session, workspace.id),
        default_model_id=default.id if default else None,
    )


@router.get(_PATH, response_model=QuickModelRead)
async def get_quick_model(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.LLM_CONFIGS_READ.value,
        "You don't have permission to view model roles in this workspace",
    )
    return await _read(session, await _workspace(session, workspace_id))


@router.put(_PATH, response_model=QuickModelRead)
async def update_quick_model(
    workspace_id: int,
    data: QuickModelUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.LLM_CONFIGS_UPDATE.value,
        "You don't have permission to update model roles in this workspace",
    )
    workspace = await _workspace(session, workspace_id)
    chosen = data.model_id
    if chosen is None:
        await session.execute(
            delete(WorkspaceQuickModel).where(
                WorkspaceQuickModel.workspace_id == workspace_id
            )
        )
    else:
        if chosen != SAME_AS_CHAT:
            usable = (
                _global_quick_model(chosen)
                if chosen < 0
                else await _db_quick_model(session, workspace, chosen, "setting")
            )
            if usable is None:
                raise HTTPException(
                    status_code=400,
                    detail="Selected model is not available for chat",
                )
        await session.execute(
            insert(WorkspaceQuickModel)
            .values(workspace_id=workspace_id, model_id=chosen)
            .on_conflict_do_update(
                index_elements=[WorkspaceQuickModel.workspace_id],
                set_={"model_id": chosen},
            )
        )
    await session.commit()
    return await _read(session, workspace)
