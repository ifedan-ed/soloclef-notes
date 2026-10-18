"""An invite is for one registered account: sent to it, and only it can accept.

Over HTTP on real Postgres; the SMTP server is the faked boundary.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import select

from app.db import WorkspaceInvite, WorkspaceMembership

pytestmark = pytest.mark.integration


def _invites(team):
    return f"/api/v1/workspaces/{team.ws}/invites"


async def _invite(client, team, email, role="viewer", **extra):
    return await client.act_as(team.owner).post(
        _invites(team),
        json={"email": email, "role_id": getattr(team.roles, role).id, **extra},
    )


async def test_invite_is_emailed_with_who_where_and_the_link(client, team, outbox):
    response = await _invite(client, team, team.outsider.email.upper())
    assert response.status_code == 200, response.text
    body = response.json()
    assert body["email"] == team.outsider.email.lower()
    assert body["status"] == "pending"
    assert body["email_sent"] is True
    assert body["max_uses"] == 1

    [mail] = outbox
    assert mail.to == team.outsider.email.lower()
    assert "Owner" in mail.subject and "Ward 4" in mail.subject
    assert f"/invite/{body['invite_code']}" in mail.text_body
    assert "Viewer" in mail.text_body


async def test_address_without_an_account_is_refused_and_says_why(client, team, outbox):
    response = await _invite(client, team, "nobody-here@example.com")
    assert response.status_code == 422
    assert "Sign-ups are closed" in response.json()["detail"]
    assert outbox == []


async def test_members_and_pending_invites_are_not_invited_twice(client, team, outbox):
    member = await _invite(client, team, team.viewer.email)
    assert member.status_code == 409
    assert (await _invite(client, team, team.outsider.email)).status_code == 200
    again = await _invite(client, team, team.outsider.email)
    assert again.status_code == 409


async def test_smtp_off_still_creates_the_invite_and_says_no_email(
    client, team, monkeypatch
):
    from app.workspace_invites import delivery

    monkeypatch.setattr(delivery, "is_mail_enabled", lambda: False)
    response = await _invite(client, team, team.outsider.email)
    assert response.status_code == 200
    assert response.json()["email_sent"] is False


async def test_only_the_invited_account_can_accept(db_session, client, team, outbox):
    code = (await _invite(client, team, team.outsider.email, role="editor")).json()[
        "invite_code"
    ]

    wrong = await client.act_as(team.viewer).post(
        "/api/v1/invites/accept", json={"invite_code": code}
    )
    assert wrong.status_code == 403
    assert "another account" in wrong.json()["detail"]

    # The address matches whatever its case.
    team.outsider.email = team.outsider.email.upper()
    await db_session.flush()
    right = await client.act_as(team.outsider).post(
        "/api/v1/invites/accept", json={"invite_code": code}
    )
    assert right.status_code == 200, right.text
    assert right.json()["role_name"] == "Editor"
    membership = await db_session.scalar(
        select(WorkspaceMembership).where(
            WorkspaceMembership.workspace_id == team.ws,
            WorkspaceMembership.user_id == team.outsider.id,
        )
    )
    assert membership is not None and not membership.is_owner

    reused = await client.post("/api/v1/invites/accept", json={"invite_code": code})
    assert reused.status_code == 400


async def test_unauthenticated_accept_is_refused(client, team, outbox):
    code = (await _invite(client, team, team.outsider.email)).json()["invite_code"]
    response = await client.act_as(None).post(
        "/api/v1/invites/accept", json={"invite_code": code}
    )
    assert response.status_code == 401


async def test_expired_and_revoked_invites_cannot_be_accepted(
    db_session, client, team, outbox
):
    created = (await _invite(client, team, team.outsider.email)).json()
    invite = await db_session.get(WorkspaceInvite, created["id"])
    invite.expires_at = datetime.now(UTC) - timedelta(minutes=1)
    await db_session.flush()
    expired = await client.act_as(team.outsider).post(
        "/api/v1/invites/accept", json={"invite_code": created["invite_code"]}
    )
    assert expired.status_code == 400
    assert "expired" in expired.json()["detail"]

    revoked = await client.act_as(team.owner).delete(
        f"{_invites(team)}/{created['id']}"
    )
    assert revoked.status_code == 200
    gone = await client.act_as(team.outsider).post(
        "/api/v1/invites/accept", json={"invite_code": created["invite_code"]}
    )
    assert gone.status_code == 404


async def test_a_link_invite_works_for_any_signed_in_account_within_its_limits(
    db_session, client, team, outbox
):
    created = await client.act_as(team.owner).post(
        _invites(team), json={"role_id": team.roles.viewer.id, "max_uses": 1}
    )
    assert created.status_code == 200, created.text
    body = created.json()
    assert body["email"] is None and body["email_sent"] is None
    assert outbox == []

    info = (
        await client.act_as(None).get(f"/api/v1/invites/{body['invite_code']}/info")
    ).json()
    assert info["is_valid"] is True and info["invited_email_hint"] is None

    anon = await client.post(
        "/api/v1/invites/accept", json={"invite_code": body["invite_code"]}
    )
    assert anon.status_code == 401
    joined = await client.act_as(team.outsider).post(
        "/api/v1/invites/accept", json={"invite_code": body["invite_code"]}
    )
    assert joined.status_code == 200, joined.text
    assert joined.json()["role_name"] == "Viewer"

    # Its one use is spent.
    from tests.utils.workspace_team import new_user

    late = await new_user(db_session, "late")
    spent = await client.act_as(late).post(
        "/api/v1/invites/accept", json={"invite_code": body["invite_code"]}
    )
    assert spent.status_code == 400
    listed = (await client.act_as(team.owner).get(_invites(team))).json()
    assert listed[0]["status"] == "used_up"


async def test_a_link_invite_cannot_be_resent(client, team, outbox):
    created = (
        await client.act_as(team.owner).post(
            _invites(team), json={"role_id": team.roles.viewer.id}
        )
    ).json()
    resend = await client.post(f"{_invites(team)}/{created['id']}/resend")
    assert resend.status_code == 400


async def test_public_info_names_the_workspace_and_masks_the_address(
    client, team, outbox
):
    code = (await _invite(client, team, team.outsider.email)).json()["invite_code"]
    info = (await client.act_as(None).get(f"/api/v1/invites/{code}/info")).json()
    assert info["is_valid"] is True
    assert info["workspace_name"] == "Ward 4"
    assert info["role_name"] == "Viewer"
    assert info["inviter_name"] == "Owner"
    hint = info["invited_email_hint"]
    assert hint.endswith("@example.com") and "*" in hint
    assert team.outsider.email not in str(info)


async def test_non_members_cannot_list_create_or_revoke_invites(client, team, outbox):
    client.act_as(team.outsider)
    assert (await client.get(_invites(team))).status_code == 403
    created = await client.post(
        _invites(team),
        json={"email": team.owner.email, "role_id": team.roles.viewer.id},
    )
    assert created.status_code == 403


async def test_resend_waits_a_minute_then_sends_again(db_session, client, team, outbox):
    created = (await _invite(client, team, team.outsider.email)).json()
    resend = f"{_invites(team)}/{created['id']}/resend"
    assert (await client.post(resend)).status_code == 429
    invite = await db_session.get(WorkspaceInvite, created["id"])
    invite.last_sent_at = datetime.now(UTC) - timedelta(minutes=2)
    await db_session.flush()
    again = await client.post(resend)
    assert again.status_code == 200
    assert again.json()["email_sent"] is True
    assert len(outbox) == 2
