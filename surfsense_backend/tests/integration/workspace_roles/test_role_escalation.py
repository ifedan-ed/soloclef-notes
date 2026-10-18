"""Nobody climbs above their own role, and nobody hands out ownership.

Over HTTP on real Postgres: invites, invite edits, role changes, removals and
custom roles all go through the same rule.
"""

from __future__ import annotations

import pytest

from app.db import WorkspaceRole
from tests.utils.workspace_team import join, new_user

pytestmark = pytest.mark.integration


def _invites(team):
    return f"/api/v1/workspaces/{team.ws}/invites"


async def _custom_role(db_session, team, name, permissions):
    role = WorkspaceRole(
        name=name,
        permissions=permissions,
        workspace_id=team.ws,
        is_system_role=False,
    )
    db_session.add(role)
    await db_session.flush()
    return role


async def test_editor_cannot_invite_as_owner(client, team, outbox):
    target = team.outsider.email
    response = await client.act_as(team.editor).post(
        _invites(team), json={"email": target, "role_id": team.roles.owner.id}
    )
    assert response.status_code == 403
    assert outbox == []


async def test_nobody_invites_as_owner_not_even_the_owner(client, team, outbox):
    response = await client.act_as(team.owner).post(
        _invites(team),
        json={"email": team.outsider.email, "role_id": team.roles.owner.id},
    )
    assert response.status_code == 403


@pytest.mark.parametrize("role", ["editor", "viewer"])
async def test_editor_may_invite_at_or_below_their_role(client, team, outbox, role):
    response = await client.act_as(team.editor).post(
        _invites(team),
        json={
            "email": team.outsider.email,
            "role_id": getattr(team.roles, role).id,
        },
    )
    assert response.status_code == 200, response.text
    assert response.json()["role"]["id"] == getattr(team.roles, role).id


async def test_editor_cannot_raise_an_invite_to_owner(client, team, outbox):
    created = await client.act_as(team.owner).post(
        _invites(team),
        json={"email": team.outsider.email, "role_id": team.roles.viewer.id},
    )
    invite_id = created.json()["id"]
    response = await client.act_as(team.editor).put(
        f"{_invites(team)}/{invite_id}", json={"role_id": team.roles.owner.id}
    )
    assert response.status_code == 403


async def test_viewer_cannot_invite_at_all(client, team, outbox):
    response = await client.act_as(team.viewer).post(
        _invites(team),
        json={"email": team.outsider.email, "role_id": team.roles.viewer.id},
    )
    assert response.status_code == 403


async def test_role_manager_cannot_promote_to_owner_or_above_themselves(
    db_session, client, team
):
    # A custom role that may manage roles but holds only Viewer rights.
    manager_role = await _custom_role(
        db_session,
        team,
        "Coordinator",
        [*team.roles.viewer.permissions, "members:manage_roles"],
    )
    manager = await new_user(db_session, "coordinator")
    await join(db_session, team.ws, manager, manager_role)
    target = f"/api/v1/workspaces/{team.ws}/members/{team.memberships.viewer}"

    client.act_as(manager)
    to_owner = await client.put(target, json={"role_id": team.roles.owner.id})
    assert to_owner.status_code == 403
    to_editor = await client.put(target, json={"role_id": team.roles.editor.id})
    assert to_editor.status_code == 403
    editor_target = f"/api/v1/workspaces/{team.ws}/members/{team.memberships.editor}"
    demote_editor = await client.put(
        editor_target, json={"role_id": team.roles.viewer.id}
    )
    assert demote_editor.status_code == 403


async def test_owner_changes_roles_but_never_to_owner(client, team):
    target = f"/api/v1/workspaces/{team.ws}/members/{team.memberships.viewer}"
    client.act_as(team.owner)
    promoted = await client.put(target, json={"role_id": team.roles.editor.id})
    assert promoted.status_code == 200, promoted.text
    assert promoted.json()["role"]["name"] == "Editor"
    to_owner = await client.put(target, json={"role_id": team.roles.owner.id})
    assert to_owner.status_code == 403


async def test_nobody_changes_their_own_role(db_session, client, team):
    manager_role = await _custom_role(
        db_session,
        team,
        "Lead",
        [*team.roles.editor.permissions, "members:manage_roles"],
    )
    lead = await new_user(db_session, "lead")
    membership_id = await join(db_session, team.ws, lead, manager_role)
    response = await client.act_as(lead).put(
        f"/api/v1/workspaces/{team.ws}/members/{membership_id}",
        json={"role_id": team.roles.editor.id},
    )
    assert response.status_code == 403


async def test_remover_cannot_remove_someone_above_them(db_session, client, team):
    remover_role = await _custom_role(
        db_session,
        team,
        "Tidy",
        [*team.roles.viewer.permissions, "members:remove"],
    )
    remover = await new_user(db_session, "tidy")
    await join(db_session, team.ws, remover, remover_role)
    client.act_as(remover)
    above = await client.delete(
        f"/api/v1/workspaces/{team.ws}/members/{team.memberships.editor}"
    )
    assert above.status_code == 403
    same = await client.delete(
        f"/api/v1/workspaces/{team.ws}/members/{team.memberships.viewer}"
    )
    assert same.status_code == 200


async def test_only_the_owner_writes_roles_and_never_a_wildcard(
    db_session, client, team
):
    roles = f"/api/v1/workspaces/{team.ws}/roles"
    writer_role = await _custom_role(
        db_session,
        team,
        "Role writer",
        [*team.roles.editor.permissions, "roles:create", "roles:update"],
    )
    writer = await new_user(db_session, "writer")
    await join(db_session, team.ws, writer, writer_role)

    by_writer = await client.act_as(writer).post(
        roles, json={"name": "Sneaky", "permissions": ["documents:read"]}
    )
    assert by_writer.status_code == 403
    raise_own = await client.put(
        f"{roles}/{writer_role.id}", json={"permissions": ["*"]}
    )
    assert raise_own.status_code == 403

    client.act_as(team.owner)
    wildcard = await client.post(roles, json={"name": "Admins", "permissions": ["*"]})
    assert wildcard.status_code == 403
    edit_owner = await client.put(
        f"{roles}/{team.roles.owner.id}", json={"permissions": ["documents:read"]}
    )
    assert edit_owner.status_code in (400, 403)
    to_editor = await client.put(
        f"{roles}/{team.roles.editor.id}", json={"permissions": ["*"]}
    )
    assert to_editor.status_code == 403


async def test_only_the_owner_changes_the_connected_repository(client, team):
    """A remote copies the workspace outside the app: Editors may not connect one."""
    body = {"provider": "gitlab", "url": "https://gitlab.example.com/x/y", "token": "t"}
    url = f"/api/v1/workspaces/{team.ws}/git-remotes"
    editor = await client.act_as(team.editor).post(url, json=body)
    assert editor.status_code == 403
    assert (await client.delete(url)).status_code == 403
    assert (await client.post(f"{url}/sync")).status_code == 403
    # The Owner gets past the gate (and is then told the workspace is not git-native).
    owner = await client.act_as(team.owner).post(url, json=body)
    assert owner.status_code != 403 or "Owner" not in owner.json()["detail"]


async def test_a_role_with_settings_rights_is_still_not_the_owner(
    db_session, client, team
):
    manager_role = await _custom_role(
        db_session,
        team,
        "Settings manager",
        [*team.roles.editor.permissions, "settings:update"],
    )
    manager = await new_user(db_session, "settings")
    await join(db_session, team.ws, manager, manager_role)
    response = await client.act_as(manager).post(
        f"/api/v1/workspaces/{team.ws}/git-remotes",
        json={"provider": "gitlab", "url": "https://gitlab.com/x/y", "token": "t"},
    )
    assert response.status_code == 403
    assert "Owner" in response.json()["detail"]
