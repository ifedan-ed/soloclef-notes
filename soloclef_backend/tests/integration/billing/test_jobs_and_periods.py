"""Studio holds follow their job; plan credits reset each month and don't roll over."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from dateutil.relativedelta import relativedelta
from sqlalchemy import select

from app.billing import jobs as billing_jobs, ledger, meter
from app.billing.errors import BillingDenied
from app.billing.meter import sibling_sessions
from app.billing.persistence.models import BillingPlan, BillingUsage
from app.billing.studio import begin_studio
from app.billing.tasks import release_stale_holds, roll_periods
from app.billing.units import credits_to_micros
from app.deliverables.jobs.service import (
    claim_deliverable_job,
    create_deliverable_job,
    fail_deliverable_job,
    request_deliverable_job_cancellation,
)

pytestmark = pytest.mark.integration


async def _plan(db_session, slug):
    return await db_session.scalar(select(BillingPlan).where(BillingPlan.slug == slug))


async def _job(db_session, workspace, user, n):
    job, _ = await create_deliverable_job(
        db_session,
        kind="artifact",
        title="Report",
        workspace_id=workspace.id,
        tool_call_id=f"test:{n}",
        request={"output": "report"},
        created_by_id=user.id,
    )
    await db_session.flush()
    return job


async def test_free_plan_cannot_start_a_video_but_can_a_report(
    db_session, db_user, set_mode
):
    await set_mode("live")
    with pytest.raises(BillingDenied) as denied:
        await begin_studio(db_user.id, "video", workspace_id=1, notebook_id=None)
    assert denied.value.code == "PLAN_STUDIO_KIND_NOT_INCLUDED"
    charge, cap = await begin_studio(
        db_user.id, "report", workspace_id=1, notebook_id=None
    )
    assert charge.mode == "live" and cap is None


async def test_video_length_is_capped_by_the_plan(db_session, db_user, set_mode):
    await set_mode("live")
    account, user = await ledger.lock_account(db_session, db_user.id)
    await ledger.change_plan(
        db_session,
        account,
        user,
        await _plan(db_session, "plus"),
        source="admin",
        reason="t",
    )
    await db_session.flush()
    _charge, cap = await begin_studio(
        db_user.id, "video", workspace_id=1, notebook_id=None
    )
    assert cap == 300


async def test_one_job_at_a_time_on_free(db_session, db_user, set_mode):
    await set_mode("live")
    await begin_studio(db_user.id, "quiz", workspace_id=1, notebook_id=None)
    with pytest.raises(BillingDenied) as denied:
        await begin_studio(db_user.id, "quiz", workspace_id=1, notebook_id=None)
    assert denied.value.code == "PLAN_TOO_MANY_JOBS"


async def test_a_failed_job_gives_its_credit_back(
    db_session, db_user, db_workspace, set_mode
):
    await set_mode("live")
    job = await _job(db_session, db_workspace, db_user, 1)
    charge, _ = await begin_studio(
        db_user.id, "report", workspace_id=db_workspace.id, notebook_id=None
    )
    await meter.attach_reference(charge, billing_jobs.deliverable_reference(job.id))
    await claim_deliverable_job(db_session, job.id)
    await fail_deliverable_job(
        db_session, job.id, failure_code="render_failed", internal_error="x"
    )
    usage = await db_session.get(BillingUsage, charge.usage_id, populate_existing=True)
    assert usage.status == "released"
    account, user = await ledger.lock_account(db_session, db_user.id)
    assert ledger.balances(account, user).spendable_micros == credits_to_micros(100)


async def test_a_cancelled_queued_job_gives_its_credit_back(
    db_session, db_user, db_workspace, set_mode
):
    await set_mode("live")
    job = await _job(db_session, db_workspace, db_user, 2)
    charge, _ = await begin_studio(
        db_user.id, "slides", workspace_id=db_workspace.id, notebook_id=None
    )
    await meter.attach_reference(charge, billing_jobs.deliverable_reference(job.id))
    await request_deliverable_job_cancellation(db_session, job.id)
    usage = await db_session.get(BillingUsage, charge.usage_id, populate_existing=True)
    assert usage.status == "released"


async def test_a_ready_job_is_charged(db_session, db_user, set_mode):
    await set_mode("live")
    charge, _ = await begin_studio(
        db_user.id, "report", workspace_id=1, notebook_id=None
    )
    await meter.attach_reference(charge, "deliverable_job:777")
    await billing_jobs.settle_reference(db_session, "deliverable_job:777")
    account, _user = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_credits_micros == credits_to_micros(80)


async def test_plan_credits_reset_monthly_without_rollover(
    db_session, db_user, set_mode
):
    await set_mode("live")
    account, user = await ledger.lock_account(db_session, db_user.id)
    account.plan_credits_micros = credits_to_micros(37)  # left over
    await ledger.grant_topup(
        db_session,
        db_user.id,
        credits_to_micros(9),
        kind="purchase",
        reason="t",
        reference="p",
    )
    account.period_end = datetime.now(UTC) - timedelta(minutes=1)
    await db_session.flush()
    rolled = await roll_periods(sibling_sessions(db_session))
    assert rolled >= 1
    account, user = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_credits_micros == credits_to_micros(100)  # not 137
    assert user.credit_micros_balance == credits_to_micros(9)  # top-ups never expire
    assert account.period_end > datetime.now(UTC)


async def test_a_prepaid_plan_falls_back_to_free_when_it_ends(
    db_session, db_user, set_mode
):
    account, user = await ledger.lock_account(db_session, db_user.id)
    await ledger.change_plan(
        db_session,
        account,
        user,
        await _plan(db_session, "pro"),
        source="crypto",
        paid_until=datetime.now(UTC) + relativedelta(months=1),
        reason="t",
    )
    account.plan_paid_until = datetime.now(UTC) - timedelta(minutes=1)
    account.period_end = datetime.now(UTC) - timedelta(minutes=1)
    await db_session.flush()
    account, user = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_id == (await _plan(db_session, "free")).id
    assert account.plan_source == "default"
    assert account.plan_credits_micros == credits_to_micros(100)


async def test_stale_holds_are_released(db_session, db_user, set_mode):
    await set_mode("live")
    charge = await meter.begin(db_user.id, "chat_standard")
    usage = await db_session.get(BillingUsage, charge.usage_id)
    usage.created_at = datetime.now(UTC) - timedelta(hours=7)
    await db_session.flush()
    assert await release_stale_holds(sibling_sessions(db_session)) == 1
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.reserved_micros == 0
