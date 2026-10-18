"""Search inside the workspace's notebooks (the notebooks home search box)."""

from __future__ import annotations

from fastapi import APIRouter, Depends, Query, Response
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session, has_permission
from app.users import get_auth_context
from app.utils.rbac import get_user_permissions

from ..access import authorize
from ..schemas import NotebookSearchRead, SearchNotebookHit, SearchPassage
from ..search.grouping import MAX_NOTEBOOKS, MAX_PASSAGES, MAX_PER_NOTEBOOK
from ..search.service import MAX_QUERY_CHARS, passage_payload, search_notebooks

router = APIRouter()


# Declared before ``/notebooks/{notebook_id}`` is mounted (see routes/__init__),
# so "search" is never parsed as a notebook id.
@router.get(
    "/workspaces/{workspace_id}/notebooks/search",
    response_model=NotebookSearchRead,
)
async def search_inside_notebooks(
    workspace_id: int,
    response: Response,
    q: str = Query("", max_length=MAX_QUERY_CHARS),
    limit: int = Query(MAX_NOTEBOOKS, ge=1, le=MAX_NOTEBOOKS),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Passages matching ``q`` from each notebook's enabled sources and notes.

    Needs the same permission as listing notebooks. Source passages quote
    document content, so they also need document read access; without it the
    search covers notes only. Studio outputs and chats are never searched.
    """
    await authorize(session, auth, workspace_id, Permission.CHATS_READ)
    granted = await get_user_permissions(session, auth.user.id, workspace_id)
    outcome = await search_notebooks(
        session,
        workspace_id,
        q,
        include_documents=has_permission(granted, Permission.DOCUMENTS_READ.value),
        max_notebooks=limit,
        per_notebook=MAX_PER_NOTEBOOK,
        max_passages=MAX_PASSAGES,
    )
    response.headers["Cache-Control"] = "private, no-store"
    hits = []
    for group in outcome.groups:
        notebook = outcome.notebooks[group.notebook_id]
        hits.append(
            SearchNotebookHit(
                id=notebook.id,
                title=notebook.title,
                icon=notebook.icon,
                updated_at=notebook.updated_at,
                passages=[
                    SearchPassage(**passage_payload(placed, outcome.terms))
                    for placed in group.passages
                ],
            )
        )
    return NotebookSearchRead(
        query=outcome.query, notebooks=hits, took_ms=outcome.took_ms
    )
