"""A workspace with an Owner, an Editor, a Viewer and an outsider, over HTTP.

Import the fixtures into a directory's conftest to use them there.
"""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest
import pytest_asyncio
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import User, Workspace, WorkspaceMembership, WorkspaceRole
from app.routes.workspaces_routes import create_default_roles_and_membership
from tests.utils.acting_client import acting_client


async def new_user(session: AsyncSession, label: str) -> User:
    user = User(
        id=uuid.uuid4(),
        email=f"{label}-{uuid.uuid4().hex[:6]}@example.com",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
        display_name=label.title(),
    )
    session.add(user)
    await session.flush()
    return user


async def role_named(session: AsyncSession, workspace_id: int, name: str):
    return await session.scalar(
        select(WorkspaceRole).where(
            WorkspaceRole.workspace_id == workspace_id, WorkspaceRole.name == name
        )
    )


async def join(session: AsyncSession, workspace_id: int, user: User, role) -> int:
    membership = WorkspaceMembership(
        user_id=user.id, workspace_id=workspace_id, role_id=role.id, is_owner=False
    )
    session.add(membership)
    await session.flush()
    return membership.id


@pytest_asyncio.fixture
async def team(db_session: AsyncSession) -> SimpleNamespace:
    owner = await new_user(db_session, "owner")
    workspace = Workspace(name="Ward 4", user_id=owner.id)
    db_session.add(workspace)
    await db_session.flush()
    await create_default_roles_and_membership(db_session, workspace.id, owner.id)
    await db_session.flush()
    roles = SimpleNamespace(
        owner=await role_named(db_session, workspace.id, "Owner"),
        editor=await role_named(db_session, workspace.id, "Editor"),
        viewer=await role_named(db_session, workspace.id, "Viewer"),
    )
    editor = await new_user(db_session, "editor")
    viewer = await new_user(db_session, "viewer")
    outsider = await new_user(db_session, "outsider")
    memberships = SimpleNamespace(
        editor=await join(db_session, workspace.id, editor, roles.editor),
        viewer=await join(db_session, workspace.id, viewer, roles.viewer),
    )
    return SimpleNamespace(
        workspace=workspace,
        ws=workspace.id,
        owner=owner,
        editor=editor,
        viewer=viewer,
        outsider=outsider,
        roles=roles,
        memberships=memberships,
    )


@pytest_asyncio.fixture
async def client(db_session: AsyncSession):
    async with acting_client(db_session) as http:
        yield http


@pytest.fixture
def outbox(monkeypatch):
    """Invitation emails land here instead of an SMTP server."""
    from app.workspace_invites import delivery

    sent: list = []

    class Box:
        async def send(self, message):
            sent.append(message)

    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: True)
    monkeypatch.setattr(delivery, "get_mailer", lambda: Box())
    return sent
