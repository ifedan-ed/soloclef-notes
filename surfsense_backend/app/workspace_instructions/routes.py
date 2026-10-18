"""Check System Instructions before they are saved (Workspace settings)."""

from __future__ import annotations

from typing import Annotated, Literal

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context
from app.utils.rbac import check_permission

from .lint import LintCode, lint_workspace_instructions
from .render import MAX_WORKSPACE_INSTRUCTIONS_CHARS

router = APIRouter()


class InstructionsLintRequest(BaseModel):
    text: Annotated[str, Field(max_length=MAX_WORKSPACE_INSTRUCTIONS_CHARS)]
    use_model: bool = True


class InstructionsLintWarning(BaseModel):
    code: LintCode
    quote: str
    source: Literal["rule", "model"]


class InstructionsLintRead(BaseModel):
    warnings: list[InstructionsLintWarning]
    model_checked: bool


@router.post(
    "/workspaces/{workspace_id}/system-instructions/lint",
    response_model=InstructionsLintRead,
)
async def lint_system_instructions(
    workspace_id: int,
    body: InstructionsLintRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
) -> InstructionsLintRead:
    """Warnings only: the page shows them inline and saving stays allowed."""
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.SETTINGS_UPDATE.value,
        "You don't have permission to change this workspace's instructions",
    )
    result = await lint_workspace_instructions(
        session, workspace_id, body.text, use_model=body.use_model
    )
    return InstructionsLintRead(
        warnings=[
            InstructionsLintWarning(code=w.code, quote=w.quote, source=w.source)
            for w in result.warnings
        ],
        model_checked=result.model_checked,
    )
