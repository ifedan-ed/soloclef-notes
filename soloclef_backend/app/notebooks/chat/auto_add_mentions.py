"""Auto-add an @-mentioned document (or folder) to a notebook chat's sources.

``retrieval_plan.py`` (the knowledge-base search tool) intersects a turn's
@-mentions with the notebook's enabled sources and drops anything outside
that scope. That's right for a mention of something genuinely outside the
notebook's topic, but wrong for "I know this doc is in my workspace, add it
and use it" — the common case of @-mentioning something during a notebook
chat. So before the turn's retrieval scope is computed, a mention of a
workspace document or folder that isn't already one of the notebook's
sources is added as one, through the same ``link_sources`` the Sources API
route uses. Plain (non-notebook) chats are untouched — this only runs when
the chat is bound to a notebook.
"""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Document, Folder, Permission, has_permission
from app.utils.rbac import get_user_permissions

from ..add_sources import link_sources
from ..persistence import Notebook, NotebookSource


async def apply_mentioned_sources(
    session: AsyncSession,
    *,
    notebook_id: int,
    workspace_id: int,
    auth: AuthContext | None,
    mentioned_document_ids: list[int] | None,
    mentioned_folder_ids: list[int] | None,
) -> bool:
    """Link any mentioned docs/folders not already a source. Returns True if
    anything was added (callers should reload the notebook's pins)."""
    doc_ids = list(dict.fromkeys(mentioned_document_ids or []))
    folder_ids = list(dict.fromkeys(mentioned_folder_ids or []))
    if not doc_ids and not folder_ids:
        return False
    if auth is None:
        return False

    permissions = await get_user_permissions(session, auth.user.id, workspace_id)
    if not has_permission(permissions, Permission.DOCUMENTS_READ.value):
        # Can't read documents in this workspace at all -- refuse silently,
        # same as a mention of something that doesn't exist.
        return False

    found_docs: set[int] = set()
    if doc_ids:
        found_docs = set(
            (
                await session.scalars(
                    select(Document.id).where(
                        Document.id.in_(doc_ids),
                        Document.workspace_id == workspace_id,
                    )
                )
            ).all()
        )
    found_folders: set[int] = set()
    if folder_ids:
        found_folders = set(
            (
                await session.scalars(
                    select(Folder.id).where(
                        Folder.id.in_(folder_ids),
                        Folder.workspace_id == workspace_id,
                    )
                )
            ).all()
        )
    if not found_docs and not found_folders:
        return False

    existing_docs: set[int] = set()
    if found_docs:
        existing_docs = set(
            (
                await session.scalars(
                    select(NotebookSource.document_id).where(
                        NotebookSource.notebook_id == notebook_id,
                        NotebookSource.document_id.in_(found_docs),
                    )
                )
            ).all()
        )
    existing_folders: set[int] = set()
    if found_folders:
        existing_folders = set(
            (
                await session.scalars(
                    select(NotebookSource.folder_id).where(
                        NotebookSource.notebook_id == notebook_id,
                        NotebookSource.folder_id.in_(found_folders),
                    )
                )
            ).all()
        )

    new_docs = sorted(found_docs - existing_docs)
    new_folders = sorted(found_folders - existing_folders)
    if not new_docs and not new_folders:
        return False

    notebook = await session.get(Notebook, notebook_id)
    if notebook is None:
        return False
    await link_sources(
        session, notebook, document_ids=new_docs, folder_ids=new_folders
    )
    return True
