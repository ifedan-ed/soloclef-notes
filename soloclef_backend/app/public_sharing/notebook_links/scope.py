"""What one live notebook link may show, decided afresh on every request.

``resolve_link`` is the only way in: the token must name a link that has not
expired, whose notebook still exists in the link's workspace, and whose
creator may still create public links there. Everything else a visitor reaches
is found *through* the notebook it returns and re-checked against it:

* sources: the notebook's document sources, plus the documents directly in its
  folder sources (the rule its chat uses), in the notebook's workspace, never
  Studio outputs and never documents being deleted;
* artifacts: the finished Studio outputs of this notebook, and what its shared
  chat saved or generated, in the notebook's workspace.
"""

from __future__ import annotations

from dataclasses import dataclass

from fastapi import HTTPException
from sqlalchemy import func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import (
    ARTIFACT_API_SOURCE,
    ChatVisibility,
    Document,
    DocumentType,
    NewChatThread,
    Permission,
    Workspace,
    has_permission,
)
from app.notebooks.persistence import Notebook, NotebookSource
from app.notebooks.studio.listing import StudioOutputRead, list_outputs
from app.utils.rbac import get_user_membership

from ..expiry import is_expired
from .models import NotebookPublicLink


def gone() -> HTTPException:
    """Unknown, revoked, deleted and out-of-scope all answer the same."""
    return HTTPException(status_code=404, detail="Not found")


@dataclass(frozen=True)
class LiveLink:
    link: NotebookPublicLink
    notebook: Notebook


async def creator_may_share(session: AsyncSession, link: NotebookPublicLink) -> bool:
    """The link lives only while the person who made it may still make it."""
    if link.created_by_user_id is None:
        return False
    membership = await get_user_membership(
        session, link.created_by_user_id, link.workspace_id
    )
    if membership is None:
        return False
    if membership.is_owner:
        return True
    permissions = membership.role.permissions if membership.role else []
    return has_permission(permissions or [], Permission.PUBLIC_SHARING_CREATE.value)


async def resolve_link(session: AsyncSession, token: str) -> LiveLink:
    """The live link and its notebook, or 404 (unknown, revoked, deleted) / 410."""
    if not token or len(token) > 128:
        raise gone()
    link = await session.scalar(
        select(NotebookPublicLink).where(NotebookPublicLink.share_token == token)
    )
    if link is None:
        raise gone()
    if is_expired(link.expires_at):
        raise HTTPException(status_code=410, detail="This link has expired")
    notebook = await session.scalar(
        select(Notebook).where(
            Notebook.id == link.notebook_id,
            Notebook.workspace_id == link.workspace_id,
        )
    )
    if notebook is None or not await creator_may_share(session, link):
        raise gone()
    return LiveLink(link=link, notebook=notebook)


def _shown(workspace_id: int):
    return (
        Document.workspace_id == workspace_id,
        Document.document_type != DocumentType.ARTIFACT,
        func.coalesce(Document.status["state"].astext, "ready") != "deleting",
    )


async def source_documents(session: AsyncSession, notebook: Notebook) -> list[Document]:
    """The documents a visitor sees in the source list, in the order added."""
    rows = (
        await session.scalars(
            select(NotebookSource).where(NotebookSource.notebook_id == notebook.id)
        )
    ).all()
    document_ids = [row.document_id for row in rows if row.document_id is not None]
    folder_ids = [row.folder_id for row in rows if row.folder_id is not None]
    if not document_ids and not folder_ids:
        return []
    documents = (
        await session.scalars(
            select(Document).where(
                *_shown(notebook.workspace_id),
                or_(
                    Document.id.in_(document_ids),
                    Document.folder_id.in_(folder_ids),
                ),
            )
        )
    ).all()
    order = {doc_id: index for index, doc_id in enumerate(document_ids)}
    return sorted(documents, key=lambda d: (order.get(d.id, len(order)), d.id))


async def source_document(
    session: AsyncSession, notebook: Notebook, document_id: int
) -> Document:
    for document in await source_documents(session, notebook):
        if document.id == document_id:
            return document
    raise gone()


async def shared_thread(
    session: AsyncSession, link: NotebookPublicLink, notebook: Notebook
) -> NewChatThread | None:
    """The chat the link's creator sees in this notebook: their newest one."""
    if link.created_by_user_id is None:
        return None
    visible = [
        NewChatThread.created_by_id == link.created_by_user_id,
        NewChatThread.visibility == ChatVisibility.SEARCH_SPACE,
    ]
    workspace_owner_id = await session.scalar(
        select(Workspace.user_id).where(Workspace.id == notebook.workspace_id)
    )
    if workspace_owner_id == link.created_by_user_id:
        visible.append(NewChatThread.created_by_id.is_(None))
    return await session.scalar(
        select(NewChatThread)
        .where(
            NewChatThread.notebook_id == notebook.id,
            NewChatThread.workspace_id == notebook.workspace_id,
            NewChatThread.source != ARTIFACT_API_SOURCE,
            NewChatThread.archived.is_(False),
            or_(*visible),
        )
        .order_by(NewChatThread.updated_at.desc(), NewChatThread.id.desc())
        .limit(1)
    )


async def finished_outputs(
    session: AsyncSession, notebook: Notebook
) -> list[StudioOutputRead]:
    """Studio outputs that are done; queued, running and failed ones stay hidden."""
    return [
        output
        for output in await list_outputs(session, notebook)
        if output.status == "ready" and output.artifact_id
    ]
