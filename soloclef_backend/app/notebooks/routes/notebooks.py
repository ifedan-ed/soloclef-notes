"""Create, list, rename and delete notebooks."""

from __future__ import annotations

from fastapi import APIRouter, Depends, Response
from sqlalchemy import delete, select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context

from ..access import authorize, authorized_notebook
from ..autoname.defaults import is_default_title
from ..autoname.pending import naming_pending
from ..copying import copy_notebook
from ..output_titles import retitle_outputs
from ..persistence import Notebook, NotebookFeature
from ..schemas import NotebookCopy, NotebookCreate, NotebookRead, NotebookUpdate
from ..source_counts import source_counts

router = APIRouter()


async def _featured_ids(
    session: AsyncSession, user_id, notebook_ids: list[int]
) -> set[int]:
    if not notebook_ids:
        return set()
    return set(
        (
            await session.scalars(
                select(NotebookFeature.notebook_id).where(
                    NotebookFeature.user_id == user_id,
                    NotebookFeature.notebook_id.in_(notebook_ids),
                )
            )
        ).all()
    )


async def _read_one(
    session: AsyncSession, notebook: Notebook, auth: AuthContext
) -> NotebookRead:
    counts = await source_counts(session, [notebook.id])
    featured = await _featured_ids(session, auth.user.id, [notebook.id])
    pending = await naming_pending(session, [notebook])
    return _read(
        notebook,
        counts.get(notebook.id),
        notebook.id in featured,
        notebook.id in pending,
    )


def _read(
    notebook: Notebook,
    counts: tuple[int, int] | None,
    featured: bool = False,
    title_pending: bool = False,
) -> NotebookRead:
    total, enabled = counts or (0, 0)
    return NotebookRead(
        id=notebook.id,
        workspace_id=notebook.workspace_id,
        title=notebook.title,
        icon=notebook.icon,
        created_at=notebook.created_at,
        updated_at=notebook.updated_at,
        source_count=total,
        enabled_source_count=enabled,
        chat_goal=notebook.chat_goal or "default",
        chat_instructions=notebook.chat_instructions,
        response_length=notebook.response_length or "default",
        featured=featured,
        title_source=notebook.title_source or "user",
        title_pending=title_pending,
    )


@router.get("/workspaces/{workspace_id}/notebooks", response_model=list[NotebookRead])
async def list_notebooks(
    workspace_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await authorize(session, auth, workspace_id, Permission.CHATS_READ)
    notebooks = (
        await session.scalars(
            select(Notebook)
            .where(Notebook.workspace_id == workspace_id)
            .order_by(Notebook.updated_at.desc(), Notebook.id.desc())
        )
    ).all()
    ids = [n.id for n in notebooks]
    counts = await source_counts(session, ids)
    featured = await _featured_ids(session, auth.user.id, ids)
    pending = await naming_pending(session, list(notebooks))
    response.headers["Cache-Control"] = "private, no-store"
    return [
        _read(n, counts.get(n.id), n.id in featured, n.id in pending) for n in notebooks
    ]


@router.post(
    "/workspaces/{workspace_id}/notebooks",
    response_model=NotebookRead,
    status_code=201,
)
async def create_notebook(
    workspace_id: int,
    body: NotebookCreate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await authorize(session, auth, workspace_id, Permission.CHATS_CREATE)
    # The apps create "Untitled notebook" in the UI language; only such a
    # default title lets the notebook name itself later.
    source = "auto" if is_default_title(body.title) else "user"
    notebook = Notebook(
        workspace_id=workspace_id,
        title=body.title,
        icon=body.icon,
        created_by_id=auth.user.id,
        title_source=source,
        icon_source=source,
    )
    session.add(notebook)
    await session.commit()
    await session.refresh(notebook)
    return _read(notebook, None)


@router.get(
    "/workspaces/{workspace_id}/notebooks/{notebook_id}",
    response_model=NotebookRead,
)
async def get_notebook(
    workspace_id: int,
    notebook_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    response.headers["Cache-Control"] = "private, no-store"
    return await _read_one(session, notebook, auth)


@router.patch(
    "/workspaces/{workspace_id}/notebooks/{notebook_id}",
    response_model=NotebookRead,
)
async def update_notebook(
    workspace_id: int,
    notebook_id: int,
    body: NotebookUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    fields = body.model_fields_set
    if "title" in fields and body.title is not None:
        if body.title != notebook.title:
            # The user named it: automatic naming stops for good.
            notebook.title_source = "user"
            await retitle_outputs(session, notebook.id, {notebook.title}, body.title)
        notebook.title = body.title
    if "icon" in fields:
        icon = (body.icon or "").strip() or None
        if icon != notebook.icon:
            notebook.icon_source = "user"
        notebook.icon = icon
    if "chat_goal" in fields and body.chat_goal is not None:
        notebook.chat_goal = body.chat_goal
    if "chat_instructions" in fields:
        notebook.chat_instructions = (body.chat_instructions or "").strip() or None
    if "response_length" in fields and body.response_length is not None:
        notebook.response_length = body.response_length
    await session.commit()
    await session.refresh(notebook)
    return await _read_one(session, notebook, auth)


@router.delete("/workspaces/{workspace_id}/notebooks/{notebook_id}", status_code=204)
async def delete_notebook(
    workspace_id: int,
    notebook_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Remove the notebook, its source links and notes; documents and chats stay."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_DELETE
    )
    await session.delete(notebook)
    await session.commit()
    return Response(status_code=204)


@router.post(
    "/workspaces/{workspace_id}/notebooks/{notebook_id}/copy",
    response_model=NotebookRead,
    status_code=201,
)
async def copy_notebook_route(
    workspace_id: int,
    notebook_id: int,
    body: NotebookCopy,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Duplicate settings, source links and notes; chats and outputs stay behind."""
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.CHATS_CREATE,
    )
    title = (body.title or "").strip() or f"{notebook.title} (copy)"
    copy = await copy_notebook(session, notebook, title=title, user_id=auth.user.id)
    return await _read_one(session, copy, auth)


_FEATURE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/feature"


@router.put(_FEATURE, response_model=NotebookRead)
async def feature_notebook(
    workspace_id: int,
    notebook_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Pin the notebook to the caller's own "Featured notebooks" row."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    await session.execute(
        pg_insert(NotebookFeature)
        .values(notebook_id=notebook.id, user_id=auth.user.id)
        .on_conflict_do_nothing()
    )
    await session.commit()
    return await _read_one(session, notebook, auth)


@router.delete(_FEATURE, response_model=NotebookRead)
async def unfeature_notebook(
    workspace_id: int,
    notebook_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    await session.execute(
        delete(NotebookFeature).where(
            NotebookFeature.notebook_id == notebook.id,
            NotebookFeature.user_id == auth.user.id,
        )
    )
    await session.commit()
    return await _read_one(session, notebook, auth)
