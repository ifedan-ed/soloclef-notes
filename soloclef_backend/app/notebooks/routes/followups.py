"""Follow-up question cards under a notebook chat answer."""

from __future__ import annotations

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context

from ..access import authorized_notebook
from ..chat.followups import followup_questions
from ..sources_listing import list_sources

router = APIRouter()


class FollowupsRequest(BaseModel):
    answer: str = Field(min_length=1, max_length=50_000)
    # The UI locale; without it the questions follow the answer's language.
    locale: str | None = Field(default=None, max_length=35)


class FollowupsRead(BaseModel):
    suggestions: list[str]


def get_followup_generator():
    """Seam for tests; ``None`` means the workspace's chat model."""
    return None


@router.post(
    "/workspaces/{workspace_id}/notebooks/{notebook_id}/followups",
    response_model=FollowupsRead,
)
async def get_followups(
    workspace_id: int,
    notebook_id: int,
    body: FollowupsRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    generate=Depends(get_followup_generator),
):
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    titles = [s.title for s in await list_sources(session, notebook) if s.enabled]
    return FollowupsRead(
        suggestions=await followup_questions(
            session,
            workspace_id=workspace_id,
            answer=body.answer,
            source_titles=titles,
            generate=generate,
            locale=body.locale,
        )
    )
