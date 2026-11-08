from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from app.auth.context import AuthContext
from app.billing.ledger import is_config_admin
from app.db import Prompt, WorkspaceMembership, get_async_session
from app.schemas.prompts import (
    PromptCreate,
    PromptRead,
    PromptUpdate,
    PublicPromptRead,
)
from app.users import require_session_context

router = APIRouter(tags=["Prompts"])


def _refuse_public_unless_admin(user, is_public: bool | None) -> None:
    """Community prompts are shown to every account, so only admins publish them.

    A prompt written for one workspace can carry what that workspace is about;
    ordinary accounts keep theirs private.
    """
    if is_public and not is_config_admin(user):
        raise HTTPException(
            status_code=403, detail="Sharing prompts with everyone is turned off"
        )


@router.get("/prompts", response_model=list[PromptRead])
async def list_prompts(
    workspace_id: int | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    user = auth.user
    query = select(Prompt).where(Prompt.user_id == user.id)
    if workspace_id is not None:
        query = query.where(Prompt.workspace_id == workspace_id)
    query = query.order_by(Prompt.created_at.desc())
    result = await session.execute(query)
    return result.scalars().all()


@router.post("/prompts", response_model=PromptRead)
async def create_prompt(
    body: PromptCreate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    user = auth.user
    _refuse_public_unless_admin(user, body.is_public)
    if body.workspace_id is not None:
        membership = await session.execute(
            select(WorkspaceMembership).where(
                WorkspaceMembership.user_id == user.id,
                WorkspaceMembership.workspace_id == body.workspace_id,
            )
        )
        if not membership.scalar_one_or_none():
            raise HTTPException(
                status_code=403,
                detail="You are not a member of this workspace",
            )

    prompt = Prompt(
        user_id=user.id,
        workspace_id=body.workspace_id,
        name=body.name,
        prompt=body.prompt,
        mode=body.mode,
        is_public=body.is_public,
    )
    session.add(prompt)
    await session.commit()
    await session.refresh(prompt)
    return prompt


@router.put("/prompts/{prompt_id}", response_model=PromptRead)
async def update_prompt(
    prompt_id: int,
    body: PromptUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    user = auth.user
    result = await session.execute(
        select(Prompt).where(
            Prompt.id == prompt_id,
            Prompt.user_id == user.id,
        )
    )
    prompt = result.scalar_one_or_none()
    if not prompt:
        raise HTTPException(status_code=404, detail="Prompt not found")

    updates = body.model_dump(exclude_unset=True)
    _refuse_public_unless_admin(
        user, updates.get("is_public") and not prompt.is_public
    )
    content_fields = {"name", "prompt", "mode"}
    has_content_change = bool(updates.keys() & content_fields)

    for field, value in updates.items():
        setattr(prompt, field, value)

    if has_content_change:
        prompt.version = Prompt.version + 1

    session.add(prompt)
    await session.commit()
    await session.refresh(prompt)
    return prompt


@router.delete("/prompts/{prompt_id}")
async def delete_prompt(
    prompt_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    user = auth.user
    result = await session.execute(
        select(Prompt).where(
            Prompt.id == prompt_id,
            Prompt.user_id == user.id,
        )
    )
    prompt = result.scalar_one_or_none()
    if not prompt:
        raise HTTPException(status_code=404, detail="Prompt not found")

    await session.delete(prompt)
    await session.commit()
    return {"success": True}


@router.get("/prompts/public", response_model=list[PublicPromptRead])
async def list_public_prompts(
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    user = auth.user
    result = await session.execute(
        select(Prompt)
        .options(selectinload(Prompt.user))
        .where(Prompt.is_public.is_(True), Prompt.user_id != user.id)
        .order_by(Prompt.created_at.desc())
    )
    prompts = result.scalars().all()
    return [
        PublicPromptRead(
            **PromptRead.model_validate(p).model_dump(),
            author_name=p.user.email if p.user else None,
        )
        for p in prompts
    ]


@router.post("/prompts/{prompt_id}/copy", response_model=PromptRead)
async def copy_public_prompt(
    prompt_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
):
    user = auth.user
    result = await session.execute(
        select(Prompt).where(
            Prompt.id == prompt_id,
            Prompt.is_public.is_(True),
        )
    )
    source = result.scalar_one_or_none()
    if not source:
        raise HTTPException(status_code=404, detail="Prompt not found")

    copy = Prompt(
        user_id=user.id,
        name=source.name,
        prompt=source.prompt,
        mode=source.mode,
        is_public=False,
    )
    session.add(copy)
    await session.commit()
    await session.refresh(copy)
    return copy
