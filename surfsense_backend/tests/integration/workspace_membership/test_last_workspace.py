"""Nobody deletes or leaves their only workspace; a second one goes as before.

Over HTTP on real Postgres. The Celery dispatch of the heavy delete is
replaced, so a successful delete leaves the "[DELETING]" marker behind.
"""

from __future__ import annotations

import pytest
from sqlalchemy import select

from app.db import Workspace, WorkspaceMembership
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.workspace_membership.last_workspace import (
    LAST_WORKSPACE_DELETE_DETAIL,
    LAST_WORKSPACE_LEAVE_DETAIL,
)
from tests.utils.workspace_team import join, new_user, role_named

pytestmark = pytest.mark.integration


@pytest.fixture
def queued(monkeypatch):
    from app.tasks.celery_tasks import document_tasks

    sent: list[int] = []
    monkeypatch.setattr(
        document_tasks.delete_workspace_task, "delay", lambda ws: sent.append(ws)
    )
    return sent


async def _own_workspace(db_session, user, name="Second") -> Workspace:
    space = Workspace(name=name, user_id=user.id)
    db_session.add(space)
    await db_session.flush()
    await create_default_roles_and_membership(db_session, space.id, user.id)
    await db_session.flush()
    return space


async def test_the_owner_cannot_delete_their_only_workspace(client, db_session, queued):
    owner = await new_user(db_session, "solo")
    only = await _own_workspace(db_session, owner, "Only")

    response = await client.act_as(owner).delete(f"/api/v1/workspaces/{only.id}")

    assert response.status_code == 409
    assert response.json()["detail"] == LAST_WORKSPACE_DELETE_DETAIL
    await db_session.refresh(only)
    assert not only.name.startswith("[DELETING]")
    assert queued == []


async def test_a_second_workspace_can_be_deleted(client, db_session, queued):
    owner = await new_user(db_session, "two")
    first = await _own_workspace(db_session, owner, "First")
    second = await _own_workspace(db_session, owner, "Second")

    response = await client.act_as(owner).delete(f"/api/v1/workspaces/{second.id}")

    assert response.status_code == 200, response.text
    assert queued == [second.id]
    listed = await client.act_as(owner).get("/api/v1/workspaces")
    assert [space["id"] for space in listed.json()] == [first.id]

    # The one left is now the last: it stays.
    again = await client.act_as(owner).delete(f"/api/v1/workspaces/{first.id}")
    assert again.status_code == 409


async def test_a_workspace_being_deleted_does_not_count_as_another(
    client, db_session, queued
):
    owner = await new_user(db_session, "pending")
    keep = await _own_workspace(db_session, owner, "Keep")
    await _own_workspace(db_session, owner, "[DELETING] Old")

    response = await client.act_as(owner).delete(f"/api/v1/workspaces/{keep.id}")

    assert response.status_code == 409


async def test_a_member_cannot_leave_their_only_workspace(client, db_session, team):
    # The team's viewer belongs to the team workspace only.
    response = await client.act_as(team.viewer).delete(
        f"/api/v1/workspaces/{team.ws}/members/me"
    )

    assert response.status_code == 409
    assert response.json()["detail"] == LAST_WORKSPACE_LEAVE_DETAIL
    still = await db_session.scalar(
        select(WorkspaceMembership).where(
            WorkspaceMembership.user_id == team.viewer.id,
            WorkspaceMembership.workspace_id == team.ws,
        )
    )
    assert still is not None


async def test_a_member_with_another_workspace_can_leave(client, db_session, team):
    await _own_workspace(db_session, team.editor, "Editor's own")

    response = await client.act_as(team.editor).delete(
        f"/api/v1/workspaces/{team.ws}/members/me"
    )

    assert response.status_code == 200, response.text


async def test_joining_a_second_workspace_lets_a_member_leave_the_first(
    client, db_session, team
):
    other_owner = await new_user(db_session, "other")
    other = await _own_workspace(db_session, other_owner, "Other")
    await join(
        db_session,
        other.id,
        team.viewer,
        await role_named(db_session, other.id, "Viewer"),
    )

    response = await client.act_as(team.viewer).delete(
        f"/api/v1/workspaces/{team.ws}/members/me"
    )

    assert response.status_code == 200, response.text
