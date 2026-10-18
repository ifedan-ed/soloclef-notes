"""Make, find, list and stop live notebook links (the signed-in side).

Who may do what mirrors chat and output links: creating one takes
``public_sharing:create`` (Owners, by default) and the right to read the
notebook; seeing one takes ``public_sharing:view``; stopping one takes
``public_sharing:delete`` or having made it.
"""

from __future__ import annotations

import secrets
from datetime import datetime

from fastapi import HTTPException
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.config import config
from app.db import Permission, User, WorkspaceMembership, has_permission
from app.notebooks.persistence import Notebook
from app.utils.rbac import check_permission, check_workspace_access

from ..expiry import expires_at_for, is_expired
from .models import NotebookPublicLink
from .scope import creator_may_share


class LiveLinkRead(BaseModel):
    id: int
    notebook_id: int
    notebook_title: str
    notebook_icon: str | None = None
    share_token: str
    public_url: str
    created_at: datetime
    expires_at: datetime | None
    is_expired: bool
    created_by_user_id: str | None
    created_by_name: str | None
    can_revoke: bool


def public_path(token: str) -> str:
    return f"/public/notebook/{token}"


def new_token() -> str:
    return secrets.token_urlsafe(48)


def _may_revoke(
    membership: WorkspaceMembership, auth: AuthContext, link: NotebookPublicLink
) -> bool:
    if link.created_by_user_id == auth.user.id or membership.is_owner:
        return True
    permissions = membership.role.permissions if membership.role else []
    return has_permission(permissions or [], Permission.PUBLIC_SHARING_DELETE.value)


async def _read(
    session: AsyncSession,
    link: NotebookPublicLink,
    notebook: Notebook,
    membership: WorkspaceMembership,
    auth: AuthContext,
) -> LiveLinkRead:
    creator = None
    if link.created_by_user_id is not None:
        creator = await session.get(User, link.created_by_user_id)
    frontend = (config.NEXT_FRONTEND_URL or "").rstrip("/")
    return LiveLinkRead(
        id=link.id,
        notebook_id=notebook.id,
        notebook_title=notebook.title,
        notebook_icon=notebook.icon,
        share_token=link.share_token,
        public_url=f"{frontend}{public_path(link.share_token)}",
        created_at=link.created_at,
        expires_at=link.expires_at,
        is_expired=is_expired(link.expires_at),
        created_by_user_id=str(link.created_by_user_id)
        if link.created_by_user_id
        else None,
        created_by_name=(creator.display_name or creator.email) if creator else None,
        can_revoke=_may_revoke(membership, auth, link),
    )


async def _link_for(
    session: AsyncSession, notebook: Notebook
) -> NotebookPublicLink | None:
    return await session.scalar(
        select(NotebookPublicLink).where(
            NotebookPublicLink.notebook_id == notebook.id,
            NotebookPublicLink.workspace_id == notebook.workspace_id,
        )
    )


async def get_link(
    session: AsyncSession, auth: AuthContext, notebook: Notebook
) -> LiveLinkRead | None:
    membership = await check_permission(
        session,
        auth,
        notebook.workspace_id,
        Permission.PUBLIC_SHARING_VIEW.value,
        "You don't have permission to view public share links",
    )
    link = await _link_for(session, notebook)
    if link is None:
        return None
    return await _read(session, link, notebook, membership, auth)


async def share_notebook(
    session: AsyncSession,
    auth: AuthContext,
    notebook: Notebook,
    expires_in_days: int | None,
    regenerate: bool,
) -> LiveLinkRead:
    """The notebook's one live link: made, or kept with a new expiry, or
    replaced by a new address (``regenerate``, or when the old one expired)."""
    membership = await check_permission(
        session,
        auth,
        notebook.workspace_id,
        Permission.PUBLIC_SHARING_CREATE.value,
        "You don't have permission to create public share links",
    )
    link = await _link_for(session, notebook)
    expires_at = expires_at_for(expires_in_days)
    if link is None:
        link = NotebookPublicLink(
            notebook_id=notebook.id,
            workspace_id=notebook.workspace_id,
            share_token=new_token(),
            created_by_user_id=auth.user.id,
            expires_at=expires_at,
        )
        session.add(link)
    else:
        if (
            regenerate
            or is_expired(link.expires_at)
            or not await creator_may_share(session, link)
        ):
            # The old address stops working at once; the link is now theirs.
            link.share_token = new_token()
            link.created_by_user_id = auth.user.id
        link.expires_at = expires_at
    await session.commit()
    await session.refresh(link)
    return await _read(session, link, notebook, membership, auth)


async def _revoke(
    session: AsyncSession,
    auth: AuthContext,
    workspace_id: int,
    link: NotebookPublicLink | None,
) -> None:
    membership = await check_workspace_access(session, auth, workspace_id)
    if link is None:
        raise HTTPException(status_code=404, detail="Public link not found")
    if not _may_revoke(membership, auth, link):
        raise HTTPException(
            status_code=403, detail="You don't have permission to revoke public links"
        )
    await session.delete(link)
    await session.commit()


async def stop_sharing(
    session: AsyncSession, auth: AuthContext, notebook: Notebook
) -> None:
    await _revoke(
        session, auth, notebook.workspace_id, await _link_for(session, notebook)
    )


async def revoke_link(
    session: AsyncSession, auth: AuthContext, workspace_id: int, link_id: int
) -> None:
    link = await session.scalar(
        select(NotebookPublicLink).where(
            NotebookPublicLink.id == link_id,
            NotebookPublicLink.workspace_id == workspace_id,
        )
    )
    await _revoke(session, auth, workspace_id, link)


async def list_links(
    session: AsyncSession, auth: AuthContext, workspace_id: int
) -> list[LiveLinkRead]:
    membership = await check_permission(
        session,
        auth,
        workspace_id,
        Permission.PUBLIC_SHARING_VIEW.value,
        "You don't have permission to view public share links",
    )
    rows = (
        await session.execute(
            select(NotebookPublicLink, Notebook)
            .join(Notebook, Notebook.id == NotebookPublicLink.notebook_id)
            .where(
                NotebookPublicLink.workspace_id == workspace_id,
                Notebook.workspace_id == workspace_id,
            )
            .order_by(NotebookPublicLink.created_at.desc())
        )
    ).all()
    return [
        await _read(session, link, notebook, membership, auth)
        for link, notebook in rows
    ]
