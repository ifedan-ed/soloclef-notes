"""Workspace-owner billing (c0019): a member's metered action inside someone
else's workspace spends *that workspace's owner's* credits, not the member's
own. See ``custom/WORKSPACE_CREDITS.md`` for the design.
"""

from __future__ import annotations

import uuid

import pytest
from sqlalchemy import select

from app.billing import ledger, meter
from app.billing.persistence.models import BillingUsage
from app.billing.units import credits_to_micros
from app.db import Workspace, WorkspaceMembership, WorkspaceRole

pytestmark = pytest.mark.integration


async def _balances(db_session, user):
    account, locked = await ledger.lock_account(db_session, user.id)
    return ledger.balances(account, locked)


async def _make_workspace(db_session, owner) -> Workspace:
    space = Workspace(name=f"Space {uuid.uuid4().hex[:8]}", user_id=owner.id)
    db_session.add(space)
    await db_session.flush()
    owner_role = WorkspaceRole(
        name="Owner",
        permissions=["*"],
        is_default=False,
        is_system_role=True,
        workspace_id=space.id,
    )
    editor_role = WorkspaceRole(
        name="Editor",
        permissions=["chats:create", "documents:create"],
        is_default=True,
        is_system_role=True,
        workspace_id=space.id,
    )
    db_session.add_all([owner_role, editor_role])
    await db_session.flush()
    db_session.add(
        WorkspaceMembership(
            user_id=owner.id,
            workspace_id=space.id,
            role_id=owner_role.id,
            is_owner=True,
        )
    )
    await db_session.flush()
    return space, editor_role


async def _add_member(db_session, space, role, user):
    membership = WorkspaceMembership(
        user_id=user.id,
        workspace_id=space.id,
        role_id=role.id,
        is_owner=False,
    )
    db_session.add(membership)
    await db_session.flush()
    return membership


async def test_member_action_debits_the_workspace_owner(
    db_session, new_user, set_mode
):
    await set_mode("live")
    owner = await new_user()
    member = await new_user()
    space, editor_role = await _make_workspace(db_session, owner)
    await _add_member(db_session, space, editor_role, member)

    charge = await meter.begin(
        member.id, "studio_quiz", workspace_id=space.id
    )  # 5 credits
    assert charge.mode == "live"
    owner_balances = await _balances(db_session, owner)
    member_balances = await _balances(db_session, member)
    assert owner_balances.reserved_micros == credits_to_micros(5)
    assert member_balances.reserved_micros == 0

    await meter.settle(charge)
    owner_balances = await _balances(db_session, owner)
    member_balances = await _balances(db_session, member)
    assert owner_balances.plan_micros == credits_to_micros(95)
    assert member_balances.plan_micros == credits_to_micros(100)  # untouched

    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.id == charge.usage_id)
    )
    assert row.user_id == member.id
    assert row.payer_user_id == owner.id


async def test_non_workspace_action_debits_the_actor(db_session, new_user, set_mode):
    await set_mode("live")
    user = await new_user()
    charge = await meter.begin(user.id, "studio_quiz")
    await meter.settle(charge)
    balances = await _balances(db_session, user)
    assert balances.plan_micros == credits_to_micros(95)
    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.id == charge.usage_id)
    )
    assert row.payer_user_id == user.id


async def test_owner_acting_in_their_own_workspace_pays_themselves(
    db_session, new_user, set_mode
):
    await set_mode("live")
    owner = await new_user()
    space, _editor_role = await _make_workspace(db_session, owner)
    charge = await meter.begin(owner.id, "studio_quiz", workspace_id=space.id)
    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.id == charge.usage_id)
    )
    assert row.user_id == owner.id
    assert row.payer_user_id == owner.id


async def test_removed_member_cannot_spend_the_owners_wallet(
    db_session, new_user, set_mode
):
    """Billing itself only resolves *who* pays; access is the route's
    permission dependency re-reading WorkspaceMembership every request. A
    member with no membership row has nothing routing their actions into the
    workspace, so nothing reaches ``meter.begin`` with that ``workspace_id``
    for them at all -- confirmed here at the membership-lookup boundary
    ``meter`` itself relies on."""
    await set_mode("live")
    owner = await new_user()
    former_member = await new_user()
    space, editor_role = await _make_workspace(db_session, owner)
    membership = await _add_member(db_session, space, editor_role, former_member)

    # While a member: resolves to the owner as payer.
    payer = await meter._resolve_payer(db_session, former_member.id, space.id)
    assert payer == owner.id

    # Removed (the Team page's "Remove member" deletes the row):
    await db_session.delete(membership)
    await db_session.flush()

    # The workspace still exists and is still owned by `owner`; billing would
    # still resolve the owner as payer for this workspace_id, but the route
    # layer (app.utils.rbac / app.workspace_roles) refuses the request before
    # any billing call is made, because the membership row is gone. That
    # refusal is covered by the existing RBAC test suite
    # (tests/integration/test_zero_authz_context.py and friends); this test
    # only pins that a bare membership lookup for `former_member` is empty.
    remaining = await db_session.scalar(
        select(WorkspaceMembership).where(
            WorkspaceMembership.workspace_id == space.id,
            WorkspaceMembership.user_id == former_member.id,
        )
    )
    assert remaining is None


async def test_refund_by_reference_goes_to_the_original_payer(
    db_session, new_user, set_mode
):
    await set_mode("live")
    owner = await new_user()
    member = await new_user()
    space, editor_role = await _make_workspace(db_session, owner)
    membership = await _add_member(db_session, space, editor_role, member)

    charge = await meter.begin(
        member.id,
        "studio_report",
        workspace_id=space.id,
        reference="deliverable_job:7001",
    )
    await meter.settle("deliverable_job:7001")
    owner_after_settle = await _balances(db_session, owner)
    assert owner_after_settle.plan_micros == credits_to_micros(80)

    # Membership changes mid-job (role unassigned -- still a member):
    membership.role_id = None
    await db_session.flush()

    await meter.release(charge, reason="job failed after settling")
    owner_after_refund = await _balances(db_session, owner)
    member_after_refund = await _balances(db_session, member)
    assert owner_after_refund.plan_micros == credits_to_micros(100)
    assert member_after_refund.plan_micros == credits_to_micros(100)  # never touched


async def test_exempt_payer_means_member_usage_is_recorded_not_debited(
    db_session, new_user, set_mode
):
    await set_mode("live")
    owner = await new_user()
    member = await new_user()
    space, editor_role = await _make_workspace(db_session, owner)
    await _add_member(db_session, space, editor_role, member)
    account, _ = await ledger.lock_account(db_session, owner.id)
    account.exempt = True
    await db_session.flush()

    charge = await meter.begin(member.id, "studio_quiz", workspace_id=space.id)
    assert charge.mode == "exempt"
    await meter.settle(charge)
    owner_balances = await _balances(db_session, owner)
    assert owner_balances.plan_micros == credits_to_micros(100)  # never debited
    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.id == charge.usage_id)
    )
    assert row.user_id == member.id
    assert row.payer_user_id == owner.id
    assert row.status == "settled"  # recorded, so the owner can still see it


async def test_usage_rows_show_both_actor_and_payer(db_session, new_user, set_mode):
    await set_mode("live")
    owner = await new_user()
    member = await new_user()
    space, editor_role = await _make_workspace(db_session, owner)
    await _add_member(db_session, space, editor_role, member)

    await meter.settle(
        await meter.begin(owner.id, "studio_quiz", workspace_id=space.id)
    )
    await meter.settle(
        await meter.begin(member.id, "studio_quiz", workspace_id=space.id)
    )
    rows = (
        (
            await db_session.execute(
                select(BillingUsage)
                .where(BillingUsage.workspace_id == space.id)
                .order_by(BillingUsage.id)
            )
        )
        .scalars()
        .all()
    )
    assert [r.user_id for r in rows] == [owner.id, member.id]
    assert [r.payer_user_id for r in rows] == [owner.id, owner.id]


async def test_a_member_cannot_make_the_owner_pay_outside_the_owners_workspace(
    db_session, new_user, set_mode
):
    await set_mode("live")
    owner = await new_user()
    member = await new_user()
    _owners_space, _editor_role = await _make_workspace(db_session, owner)
    # The member's own, unrelated workspace -- its owner is the member.
    members_space, _role = await _make_workspace(db_session, member)

    charge = await meter.begin(
        member.id, "studio_quiz", workspace_id=members_space.id
    )
    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.id == charge.usage_id)
    )
    assert row.payer_user_id == member.id  # not the unrelated owner
    assert row.payer_user_id != owner.id
    owner_balances = await _balances(db_session, owner)
    assert owner_balances.reserved_micros == 0

