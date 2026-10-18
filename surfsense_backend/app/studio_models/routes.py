"""Read, change and reset a workspace's Studio outputs model (Settings -> Models)."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import delete, func
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, Workspace, get_async_session
from app.users import get_auth_context
from app.utils.rbac import check_permission

from .catalog import named_model, usable_model
from .choices import StudioChoice, StudioModelSettings
from .model import WorkspaceStudioModel
from .recommended import RECOMMENDED_DEFAULT
from .resolution import load_settings, unavailable_entries
from .slots import STUDIO_SLOTS

router = APIRouter()
_PATH = "/workspaces/{workspace_id}/studio-model"


class RecommendedModel(BaseModel):
    name: str
    # This workspace's role id for it; None when it cannot reach the model.
    id: int | None


class StudioModelRead(BaseModel):
    default: StudioChoice
    overrides: dict[str, StudioChoice]
    # False while the workspace is on the recommended setup (no stored row).
    customized: bool
    recommended_primary: RecommendedModel
    recommended_fallback: RecommendedModel | None
    # "default" and the output types whose chosen model cannot be used now;
    # those run on the next level down (type -> default -> Auto).
    unavailable: list[str]
    slots: list[str] = list(STUDIO_SLOTS)


async def _workspace(session: AsyncSession, workspace_id: int) -> Workspace:
    workspace = await session.get(Workspace, workspace_id)
    if workspace is None:
        raise HTTPException(status_code=404, detail="Workspace not found")
    return workspace


async def _recommended(session: AsyncSession, workspace: Workspace, name: str | None):
    if name is None:
        return None
    model = await named_model(session, workspace, name)
    return RecommendedModel(name=name, id=model.id if model else None)


async def _read(session: AsyncSession, workspace: Workspace) -> StudioModelRead:
    settings, customized = await load_settings(session, workspace)
    primary_name, fallback_name = RECOMMENDED_DEFAULT
    return StudioModelRead(
        default=settings.default,
        overrides=dict(settings.overrides),
        customized=customized,
        recommended_primary=await _recommended(session, workspace, primary_name),
        recommended_fallback=await _recommended(session, workspace, fallback_name),
        unavailable=await unavailable_entries(session, workspace, settings),
    )


async def _allowed(
    session: AsyncSession, auth: AuthContext, workspace_id: int, permission: str
) -> None:
    await check_permission(
        session,
        auth,
        workspace_id,
        permission,
        "You don't have permission to "
        + ("view" if permission == Permission.LLM_CONFIGS_READ.value else "update")
        + " model roles in this workspace",
    )


@router.get(_PATH, response_model=StudioModelRead)
async def get_studio_model(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await _allowed(session, auth, workspace_id, Permission.LLM_CONFIGS_READ.value)
    return await _read(session, await _workspace(session, workspace_id))


@router.put(_PATH, response_model=StudioModelRead)
async def update_studio_model(
    workspace_id: int,
    data: StudioModelSettings,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await _allowed(session, auth, workspace_id, Permission.LLM_CONFIGS_UPDATE.value)
    workspace = await _workspace(session, workspace_id)
    for choice in (data.default, *data.overrides.values()):
        for role_id in choice.model_ids():
            if await usable_model(session, workspace, role_id) is None:
                raise HTTPException(
                    status_code=400,
                    detail="Selected model is not available for chat",
                )
    values = {
        "default_choice": data.default.model_dump(),
        "overrides": {
            slot: choice.model_dump() for slot, choice in data.overrides.items()
        },
    }
    await session.execute(
        insert(WorkspaceStudioModel)
        .values(workspace_id=workspace_id, **values)
        .on_conflict_do_update(
            index_elements=[WorkspaceStudioModel.workspace_id],
            set_={**values, "updated_at": func.now()},
        )
    )
    await session.commit()
    return await _read(session, workspace)


@router.delete(_PATH, response_model=StudioModelRead)
async def reset_studio_model(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Back to the recommended setup."""
    await _allowed(session, auth, workspace_id, Permission.LLM_CONFIGS_UPDATE.value)
    workspace = await _workspace(session, workspace_id)
    await session.execute(
        delete(WorkspaceStudioModel).where(
            WorkspaceStudioModel.workspace_id == workspace_id
        )
    )
    await session.commit()
    return await _read(session, workspace)
