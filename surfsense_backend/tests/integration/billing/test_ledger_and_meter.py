"""The wallet: grants, holds, debits in the right order, refunds, modes."""

from __future__ import annotations

import pytest
from sqlalchemy import func, select

from app.billing import ledger, meter
from app.billing.entitlements import Requirement
from app.billing.errors import BillingDenied
from app.billing.persistence.models import (
    BillingAccount,
    BillingLedgerEntry,
    BillingUsage,
)
from app.billing.tasks import check_ledger
from app.billing.units import credits_to_micros

pytestmark = pytest.mark.integration


async def _balances(db_session, user):
    account, locked = await ledger.lock_account(db_session, user.id)
    return ledger.balances(account, locked)


async def test_a_new_account_starts_on_free_with_its_monthly_credits(
    db_session, new_user
):
    user = await new_user()
    b = await _balances(db_session, user)
    assert b.plan_micros == credits_to_micros(100)
    grants = await db_session.scalar(
        select(func.count())
        .select_from(BillingLedgerEntry)
        .where(
            BillingLedgerEntry.user_id == user.id,
            BillingLedgerEntry.kind == "plan_grant",
        )
    )
    assert grants == 1


async def test_live_spend_takes_plan_credits_first_then_topup(
    db_session, new_user, set_mode
):
    await set_mode("live")
    user = await new_user()
    await ledger.grant_topup(
        db_session,
        user.id,
        credits_to_micros(50),
        kind="purchase",
        reason="t",
        reference="t1",
    )
    account, _u = await ledger.lock_account(db_session, user.id)
    account.plan_credits_micros = credits_to_micros(3)
    await db_session.flush()

    charge = await meter.begin(user.id, "studio_quiz")  # 5 credits
    assert charge.mode == "live"
    b = await _balances(db_session, user)
    assert b.reserved_micros == credits_to_micros(5)
    await meter.settle(charge)
    b = await _balances(db_session, user)
    assert b.plan_micros == 0
    assert b.topup_micros == credits_to_micros(48)
    assert b.reserved_micros == 0


async def test_live_refuses_when_credits_are_short(db_session, new_user, set_mode):
    await set_mode("live")
    user = await new_user()
    with pytest.raises(BillingDenied) as denied:
        await meter.begin(user.id, "studio_report", units=6)  # 120 > 100
    assert denied.value.code == "INSUFFICIENT_CREDITS"
    assert denied.value.required_micros == credits_to_micros(120)
    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.user_id == user.id)
    )
    assert row.status == "denied"


async def test_live_refuses_what_the_plan_does_not_include(
    db_session, new_user, set_mode
):
    await set_mode("live")
    user = await new_user()
    with pytest.raises(BillingDenied) as denied:
        await meter.begin(
            user.id, "chat_premium", need=Requirement(model_tier="premium")
        )
    assert denied.value.code == "PLAN_MODEL_TIER_NOT_INCLUDED"
    with pytest.raises(BillingDenied) as denied:
        await meter.begin(
            user.id,
            "studio_video",
            need=Requirement(studio_kind="video", video_seconds=1),
        )
    assert denied.value.code == "PLAN_STUDIO_KIND_NOT_INCLUDED"


async def test_a_failed_action_gets_its_hold_back(db_session, new_user, set_mode):
    await set_mode("live")
    user = await new_user()
    with pytest.raises(RuntimeError):
        async with meter.metered(user.id, "studio_mindmap"):
            raise RuntimeError("render failed")
    b = await _balances(db_session, user)
    assert b.reserved_micros == 0
    assert b.plan_micros == credits_to_micros(100)
    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.user_id == user.id)
    )
    assert row.status == "released"


async def test_a_settled_action_that_fails_later_is_refunded(
    db_session, new_user, set_mode
):
    await set_mode("live")
    user = await new_user()
    charge = await meter.begin(
        user.id, "studio_report", reference="deliverable_job:991"
    )
    await meter.settle("deliverable_job:991")
    assert (await _balances(db_session, user)).plan_micros == credits_to_micros(80)
    await meter.release(charge, reason="broken")
    assert (await _balances(db_session, user)).plan_micros == credits_to_micros(100)
    refunds = await db_session.scalar(
        select(func.count())
        .select_from(BillingLedgerEntry)
        .where(
            BillingLedgerEntry.kind == "refund", BillingLedgerEntry.user_id == user.id
        )
    )
    assert refunds == 1


async def test_settle_and_hold_are_idempotent_by_reference(
    db_session, new_user, set_mode
):
    await set_mode("live")
    user = await new_user()
    first = await meter.begin(user.id, "studio_quiz", reference="deliverable_job:5")
    again = await meter.begin(user.id, "studio_quiz", reference="deliverable_job:5")
    assert first.usage_id == again.usage_id
    await meter.settle("deliverable_job:5")
    await meter.settle("deliverable_job:5")
    assert (await _balances(db_session, user)).plan_micros == credits_to_micros(95)


async def test_settle_reprices_by_actual_units(db_session, new_user, set_mode):
    await set_mode("live")
    user = await new_user()
    charge = await meter.begin(user.id, "cloud_page", units=10)
    await meter.settle(charge, units=4)
    assert (await _balances(db_session, user)).plan_micros == credits_to_micros(92)


async def test_shadow_records_what_live_would_charge_and_refuse(
    db_session, new_user, set_mode
):
    await set_mode("shadow")
    user = await new_user()
    charge = await meter.begin(
        user.id, "studio_video", need=Requirement(studio_kind="video", video_seconds=1)
    )
    await meter.settle(charge)
    row = await db_session.scalar(
        select(BillingUsage).where(BillingUsage.id == charge.usage_id)
    )
    assert row.mode == "shadow"
    assert row.charged_micros == credits_to_micros(40)
    assert row.denial_code == "PLAN_STUDIO_KIND_NOT_INCLUDED"
    assert (await _balances(db_session, user)).plan_micros == credits_to_micros(100)


async def test_off_records_nothing(db_session, new_user, set_mode):
    await set_mode("off")
    user = await new_user()
    charge = await meter.begin(user.id, "studio_video")
    assert charge.usage_id is None
    assert (
        await db_session.scalar(
            select(func.count())
            .select_from(BillingUsage)
            .where(BillingUsage.user_id == user.id)
        )
        == 0
    )


async def test_exempt_accounts_are_never_refused_or_charged(
    db_session, new_user, set_mode
):
    await set_mode("live")
    owner = await new_user("owner@surfsense.net")
    for _ in range(30):  # 30 x 40 credits, far past any balance
        charge = await meter.begin(
            owner.id,
            "studio_video",
            need=Requirement(studio_kind="video", video_seconds=1),
        )
        await meter.settle(charge)
    assert charge.mode == "exempt"
    assert (await _balances(db_session, owner)).plan_micros == credits_to_micros(100)
    account = await db_session.get(BillingAccount, owner.id)
    assert account.exempt  # admins start exempt


async def test_an_admin_marked_exempt_account_is_not_refused(
    db_session, new_user, set_mode
):
    await set_mode("live")
    user = await new_user()
    account, _ = await ledger.lock_account(db_session, user.id)
    account.exempt = True
    account.plan_credits_micros = 0
    await db_session.flush()
    charge = await meter.begin(user.id, "studio_report")
    assert charge.mode == "exempt"


async def test_the_ledger_sums_to_the_balances(db_session, new_user, set_mode):
    await set_mode("live")
    user = await new_user()
    await ledger.grant_topup(
        db_session,
        user.id,
        credits_to_micros(7),
        kind="purchase",
        reason="t",
        reference="x",
    )
    charge = await meter.begin(user.id, "studio_report")
    await meter.settle(charge)
    await meter.record(user.id, "cloud_page", units=3)
    from app.billing.meter import sibling_sessions

    drifted = await check_ledger(sibling_sessions(db_session))
    assert str(user.id) not in drifted
