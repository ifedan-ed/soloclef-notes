"""Billing's periodic work (Celery beat).

* ``billing.roll_periods`` (hourly): start the next monthly period for plans
  the app renews itself (default, crypto-prepaid, admin-given): what is left
  of the plan credits expires and the new allowance is granted. Accounts also
  roll lazily on their next metered action; this keeps idle ones current.
* ``billing.release_stale_holds`` (every 15 min): give back holds whose action
  never reported back (a crashed worker), except those of jobs still running.
* ``billing.reconcile_payments`` (every 10 min): settle Stripe checkouts and
  crypto payments whose webhook never arrived, from the provider's own record.
* ``billing.check_ledger`` (daily): log any account whose cached balances
  differ from the sum of its ledger.
"""

from __future__ import annotations

import logging
from datetime import UTC, datetime, timedelta

from sqlalchemy import func, select

from app.celery_app import celery_app
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

logger = logging.getLogger(__name__)


def _factory():
    return get_celery_session_maker()


@celery_app.task(name="billing.roll_periods")
def roll_periods_task():
    return run_async_celery_task(roll_periods)


async def roll_periods(session_factory=None) -> int:
    from . import ledger
    from .persistence.models import BillingAccount

    factory = session_factory or _factory()
    async with factory() as session:
        due = (
            (
                await session.execute(
                    select(BillingAccount.user_id).where(
                        BillingAccount.period_end <= datetime.now(UTC),
                        BillingAccount.plan_source != "stripe",
                    )
                )
            )
            .scalars()
            .all()
        )
    rolled = 0
    for user_id in due:
        async with factory() as session:
            await ledger.lock_account(session, user_id)
            await session.commit()
            rolled += 1
    return rolled


@celery_app.task(name="billing.release_stale_holds")
def release_stale_holds_task():
    return run_async_celery_task(release_stale_holds)


async def release_stale_holds(session_factory=None) -> int:
    from app.db import DeliverableJob, DeliverableJobStatus

    from . import meter
    from .persistence.models import BillingUsage
    from .settings_store import load_settings

    factory = session_factory or _factory()
    async with factory() as session:
        hours = float((await load_settings(session)).get("stale_hold_hours") or 6)
        stale = (
            await session.execute(
                select(BillingUsage.id, BillingUsage.reference).where(
                    BillingUsage.status == "reserved",
                    BillingUsage.created_at
                    < datetime.now(UTC) - timedelta(hours=hours),
                )
            )
        ).all()
        running = set()
        job_ids = [
            int(ref.split(":", 1)[1])
            for _id, ref in stale
            if ref
            and ref.startswith("deliverable_job:")
            and ref.split(":", 1)[1].isdigit()
        ]
        if job_ids:
            running = set(
                (
                    await session.execute(
                        select(DeliverableJob.id).where(
                            DeliverableJob.id.in_(job_ids),
                            DeliverableJob.status.in_(
                                [
                                    DeliverableJobStatus.QUEUED,
                                    DeliverableJobStatus.RUNNING,
                                    DeliverableJobStatus.CANCELLING,
                                ]
                            ),
                        )
                    )
                ).scalars()
            )
    released = 0
    for usage_id, ref in stale:
        if (
            ref
            and ref.startswith("deliverable_job:")
            and ref.split(":", 1)[1].isdigit()
            and int(ref.split(":", 1)[1]) in running
        ):
            continue
        await meter.release(
            meter.Charge(usage_id, "live", ""),
            reason="stale hold",
            session_factory=factory,
        )
        released += 1
    return released


@celery_app.task(name="billing.reconcile_payments")
def reconcile_payments_task():
    return run_async_celery_task(reconcile_payments)


async def reconcile_payments(session_factory=None, *, stripe=None, gateway=None) -> int:
    from .crypto import provider as crypto_p
    from .crypto.gateway import crypto_provider
    from .crypto.settlement import apply_update
    from .persistence.models import BillingPayment
    from .settings_store import load_settings
    from .stripe_billing.client import stripe_client
    from .stripe_billing.events import close_checkout, fulfil_checkout

    factory = session_factory or _factory()
    cutoff = datetime.now(UTC) - timedelta(minutes=10)
    handled = 0
    async with factory() as session:
        settings = await load_settings(session)
        pending = (
            await session.execute(
                select(
                    BillingPayment.id,
                    BillingPayment.provider,
                    BillingPayment.provider_ref,
                    BillingPayment.kind,
                )
                .where(
                    BillingPayment.status.in_(["pending", "partial"]),
                    BillingPayment.created_at < cutoff,
                )
                .order_by(BillingPayment.created_at)
                .limit(200)
            )
        ).all()
    for _payment_id, provider, ref, _kind in pending:
        try:
            async with factory() as session:
                if provider == "stripe":
                    if not settings.get("stripe_secret_key"):
                        continue
                    if not ref.startswith("cs_"):
                        continue
                    client = stripe or stripe_client(settings)
                    checkout = client.v1.checkout.sessions.retrieve(ref)
                    if getattr(checkout, "status", None) == "expired":
                        await close_checkout(session, checkout, "expired")
                    else:
                        await fulfil_checkout(session, checkout)
                else:
                    gw = gateway or crypto_provider(settings)
                    update = await gw.poll(ref)
                    await apply_update(
                        session,
                        gw,
                        update,
                    )
                await session.commit()
                handled += 1
        except crypto_p.ProviderUnavailable:
            logger.info("crypto reconciliation: provider unavailable for %s", ref)
        except Exception:
            logger.exception(
                "billing reconciliation failed for %s payment %s", provider, ref
            )
    return handled


@celery_app.task(name="billing.check_ledger")
def check_ledger_task():
    return run_async_celery_task(check_ledger)


async def check_ledger(session_factory=None) -> list[str]:
    """Accounts whose cached balances disagree with their ledger (logged)."""
    from app.db import User

    from .persistence.models import BillingAccount, BillingLedgerEntry

    factory = session_factory or _factory()
    async with factory() as session:
        ledger_rows = (
            await session.execute(
                select(
                    BillingLedgerEntry.user_id,
                    BillingLedgerEntry.bucket,
                    func.sum(BillingLedgerEntry.delta_micros).label("total"),
                ).group_by(BillingLedgerEntry.user_id, BillingLedgerEntry.bucket)
            )
        ).all()
        sums = {(r.user_id, r.bucket): int(r.total) for r in ledger_rows}
        rows = (
            await session.execute(
                select(
                    BillingAccount.user_id,
                    BillingAccount.plan_credits_micros,
                    User.credit_micros_balance,
                ).join(User, User.id == BillingAccount.user_id)
            )
        ).all()
    drifted = []
    for user_id, plan, topup in rows:
        if sums.get((user_id, "plan"), 0) != int(plan) or sums.get(
            (user_id, "topup"), 0
        ) != int(topup):
            drifted.append(str(user_id))
            logger.warning(
                "billing ledger drift user=%s plan=%s ledger_plan=%s topup=%s ledger_topup=%s",
                user_id,
                plan,
                sums.get((user_id, "plan"), 0),
                topup,
                sums.get((user_id, "topup"), 0),
            )
    return drifted
