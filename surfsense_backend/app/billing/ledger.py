"""The wallet: accounts, periods, and every movement of credit.

Two buckets per person:

* **plan** — ``billing_accounts.plan_credits_micros``: the plan's monthly
  allowance. Granted at the start of each period, and whatever is left at the
  end expires (no rollover).
* **topup** — ``user.credit_micros_balance``: bought packs, the signup credit,
  incentives and admin grants. Never expires.

Spending takes plan credits first, then top-up. ``spendable = plan + topup -
reserved``, where ``reserved`` is the sum of open holds.

Every change to either bucket goes through :func:`post`, which writes a
``billing_ledger`` row carrying the bucket's balance after the change. Rows are
idempotent on ``(kind, reference)``: posting the same invoice twice is a no-op.

Concurrency: every function here takes the account row ``FOR UPDATE`` first
(:func:`lock_account`), so two requests for one person are serialized — two
parallel reserves cannot both spend the same credit. Functions take the
caller's session and never commit; the meter and the payment handlers own
their transactions.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import UTC, datetime
from uuid import UUID

from dateutil.relativedelta import relativedelta
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config
from app.db import User

from .persistence.models import BillingAccount, BillingLedgerEntry, BillingPlan

logger = logging.getLogger(__name__)

PLAN = "plan"
TOPUP = "topup"


def now_utc() -> datetime:
    return datetime.now(UTC)


def admin_emails() -> set[str]:
    raw = getattr(config, "BILLING_ADMIN_EMAILS", "") or ""
    return {part.strip().lower() for part in raw.split(",") if part.strip()}


def is_listed_admin(email: str | None, *, verified: bool) -> bool:
    """Whether ``BILLING_ADMIN_EMAILS`` names the account with ``email``.

    Only once that account's own address is verified (``user.is_verified``):
    the list names an address, and an account made with the password
    sign-up form is unverified (no mail confirms it), so anyone who signed
    up with the administrator's address before the administrator did would
    otherwise hold the billing, announcement and MCP allowlist admin pages.
    Accounts made by the operator (``scripts/create_account.py``) or by a
    single sign-on are verified; migration c0030 verified the ones linked to
    SSO before that rule. custom/README.md, "Security fixes of 2026-10-11",
    follow-up.
    """
    return verified and (email or "").lower() in admin_emails()


def is_config_admin(user: User | None) -> bool:
    if user is None:
        return False
    return bool(getattr(user, "is_superuser", False)) or is_listed_admin(
        user.email, verified=bool(getattr(user, "is_verified", False))
    )


@dataclass
class Balances:
    plan_micros: int
    topup_micros: int
    reserved_micros: int

    @property
    def spendable_micros(self) -> int:
        return self.plan_micros + self.topup_micros - self.reserved_micros


async def default_plan(session: AsyncSession) -> BillingPlan:
    plan = await session.scalar(
        select(BillingPlan)
        .where(BillingPlan.is_default.is_(True))
        .order_by(BillingPlan.id)
        .limit(1)
    )
    if plan is None:
        plan = await session.scalar(
            select(BillingPlan).order_by(BillingPlan.sort_order).limit(1)
        )
    if plan is None:
        # A database created without the migration (tests, fresh installs).
        from .seed import ensure_catalog

        await ensure_catalog(session)
        plan = await session.scalar(
            select(BillingPlan).where(BillingPlan.is_default.is_(True)).limit(1)
        )
    return plan


async def _lock_user(session: AsyncSession, user_id: UUID) -> User:
    user = (
        (
            await session.execute(
                select(User)
                .where(User.id == user_id)
                .with_for_update(of=User)
                .execution_options(populate_existing=True)
            )
        )
        .unique()
        .scalar_one_or_none()
    )
    if user is None:
        raise ValueError(f"User {user_id} not found")
    return user


async def post(
    session: AsyncSession,
    account: BillingAccount,
    user: User,
    *,
    bucket: str,
    delta: int,
    kind: str,
    reason: str | None = None,
    reference: str | None = None,
    usage_id: int | None = None,
    actor: UUID | None = None,
) -> BillingLedgerEntry | None:
    """Move ``delta`` micros in one bucket and record it. Caller holds the lock.

    Returns ``None`` (and changes nothing) when ``(kind, reference)`` was
    already posted.
    """
    if reference is not None:
        seen = await session.scalar(
            select(BillingLedgerEntry.id).where(
                BillingLedgerEntry.kind == kind,
                BillingLedgerEntry.reference == reference,
            )
        )
        if seen is not None:
            return None
    if bucket == PLAN:
        account.plan_credits_micros = int(account.plan_credits_micros) + int(delta)
        after = account.plan_credits_micros
    elif bucket == TOPUP:
        user.credit_micros_balance = int(user.credit_micros_balance) + int(delta)
        after = user.credit_micros_balance
    else:
        raise ValueError(f"Unknown bucket {bucket}")
    account.updated_at = now_utc()
    entry = BillingLedgerEntry(
        user_id=account.user_id,
        bucket=bucket,
        delta_micros=int(delta),
        balance_after_micros=int(after),
        kind=kind,
        reason=reason,
        reference=reference,
        usage_id=usage_id,
        actor_user_id=actor,
        created_at=now_utc(),
    )
    session.add(entry)
    await session.flush()
    return entry


async def _start_period(
    session: AsyncSession,
    account: BillingAccount,
    user: User,
    plan: BillingPlan,
    start: datetime,
    *,
    reason: str,
) -> None:
    """Expire what is left of the plan bucket and grant ``plan`` from ``start``."""
    if account.plan_credits_micros:
        await post(
            session,
            account,
            user,
            bucket=PLAN,
            delta=-int(account.plan_credits_micros),
            kind="plan_expire",
            reason=reason,
            reference=f"expire:{account.user_id}:{account.period_start.isoformat()}",
        )
    account.plan_id = plan.id
    account.period_start = start
    account.period_end = start + relativedelta(months=1)
    await post(
        session,
        account,
        user,
        bucket=PLAN,
        delta=int(plan.monthly_credits_micros),
        kind="plan_grant",
        reason=reason,
        reference=f"grant:{account.user_id}:{start.isoformat()}",
    )


async def roll_period_if_due(
    session: AsyncSession, account: BillingAccount, user: User
) -> None:
    """Start the next period for plans the app renews itself.

    Stripe subscriptions are renewed by their ``invoice.paid`` webhook instead.
    A prepaid (crypto) or admin plan whose paid time is over drops back to the
    default plan here.
    """
    now = now_utc()
    if account.plan_source == "stripe" or now < account.period_end:
        return
    plan = await session.get(BillingPlan, account.plan_id)
    paid_until = account.plan_paid_until
    start = account.period_end
    # Skip whole missed periods rather than granting each one.
    while start + relativedelta(months=1) <= now:
        start = start + relativedelta(months=1)
    if account.plan_source in {"crypto", "admin"} and paid_until and paid_until <= now:
        plan = await default_plan(session)
        account.plan_source = "default"
        account.plan_paid_until = None
        account.plan_status = "active"
        start = now
        reason = "Prepaid plan ended"
    else:
        reason = "Monthly renewal"
    if plan is None or not plan.active:
        plan = await default_plan(session)
        account.plan_source = "default"
    await _start_period(session, account, user, plan, start, reason=reason)


async def lock_account(
    session: AsyncSession, user_id: UUID, *, roll: bool = True
) -> tuple[BillingAccount, User]:
    """The person's account row, locked, created on first use.

    A new account starts on the default plan with its first allowance granted;
    admins (``BILLING_ADMIN_EMAILS`` or superusers) start exempt.
    """
    user = await _lock_user(session, user_id)
    # populate_existing: the row under the lock, never a stale identity-map copy.
    account = await session.scalar(
        select(BillingAccount)
        .where(BillingAccount.user_id == user_id)
        .with_for_update()
        .execution_options(populate_existing=True)
    )
    if account is None:
        plan = await default_plan(session)
        start = now_utc()
        exempt = is_config_admin(user)
        await session.execute(
            pg_insert(BillingAccount)
            .values(
                user_id=user_id,
                plan_id=plan.id,
                plan_source="default",
                plan_status="active",
                period_start=start,
                period_end=start + relativedelta(months=1),
                plan_credits_micros=0,
                reserved_micros=0,
                exempt=exempt,
                exempt_reason="Administrator" if exempt else None,
                created_at=start,
                updated_at=start,
            )
            .on_conflict_do_nothing(index_elements=["user_id"])
        )
        account = await session.scalar(
            select(BillingAccount)
            .where(BillingAccount.user_id == user_id)
            .with_for_update()
            .execution_options(populate_existing=True)
        )
        await post(
            session,
            account,
            user,
            bucket=PLAN,
            delta=int(plan.monthly_credits_micros),
            kind="plan_grant",
            reason="First period",
            reference=f"grant:{user_id}:{account.period_start.isoformat()}",
        )
    elif roll:
        await roll_period_if_due(session, account, user)
    return account, user


def balances(account: BillingAccount, user: User) -> Balances:
    return Balances(
        plan_micros=int(account.plan_credits_micros),
        topup_micros=int(user.credit_micros_balance),
        reserved_micros=int(account.reserved_micros),
    )


async def debit(
    session: AsyncSession,
    account: BillingAccount,
    user: User,
    micros: int,
    *,
    reference: str,
    usage_id: int | None,
    reason: str,
) -> int:
    """Spend ``micros``: plan bucket first, the rest from top-up.

    The top-up bucket may go slightly negative when an action cost more than
    its hold (more pages than estimated); the next top-up covers it.
    """
    if micros <= 0:
        return 0
    from_plan = min(int(micros), max(0, int(account.plan_credits_micros)))
    rest = int(micros) - from_plan
    if from_plan:
        await post(
            session,
            account,
            user,
            bucket=PLAN,
            delta=-from_plan,
            kind="debit",
            reason=reason,
            reference=f"{reference}:plan",
            usage_id=usage_id,
        )
    if rest:
        await post(
            session,
            account,
            user,
            bucket=TOPUP,
            delta=-rest,
            kind="debit",
            reason=reason,
            reference=f"{reference}:topup",
            usage_id=usage_id,
        )
    return int(micros)


async def change_plan(
    session: AsyncSession,
    account: BillingAccount,
    user: User,
    plan: BillingPlan,
    *,
    source: str,
    paid_until: datetime | None = None,
    reason: str,
    new_period: bool = True,
    period_end: datetime | None = None,
) -> None:
    """Put the account on ``plan`` now.

    ``new_period`` expires what is left and grants the new plan's allowance
    from now (a fresh purchase). Otherwise the current period continues and
    the plan bucket moves by the difference in allowances, never below zero
    (an upgrade or downgrade mid-period).
    """
    old_plan = await session.get(BillingPlan, account.plan_id)
    account.plan_source = source
    account.plan_paid_until = paid_until
    account.plan_status = "active"
    if new_period:
        await _start_period(session, account, user, plan, now_utc(), reason=reason)
    else:
        difference = int(plan.monthly_credits_micros) - int(
            old_plan.monthly_credits_micros if old_plan else 0
        )
        account.plan_id = plan.id
        if difference > 0:
            await post(
                session,
                account,
                user,
                bucket=PLAN,
                delta=difference,
                kind="plan_adjust",
                reason=reason,
            )
        elif (
            difference < 0 and account.plan_credits_micros > plan.monthly_credits_micros
        ):
            await post(
                session,
                account,
                user,
                bucket=PLAN,
                delta=int(plan.monthly_credits_micros)
                - int(account.plan_credits_micros),
                kind="plan_adjust",
                reason=reason,
            )
    if period_end is not None:
        account.period_end = period_end


async def grant_topup(
    session: AsyncSession,
    user_id: UUID,
    micros: int,
    *,
    kind: str,
    reason: str,
    reference: str | None,
    actor: UUID | None = None,
) -> BillingLedgerEntry | None:
    """Add (or with a negative amount, remove) top-up credit, recorded."""
    account, user = await lock_account(session, user_id)
    return await post(
        session,
        account,
        user,
        bucket=TOPUP,
        delta=int(micros),
        kind=kind,
        reason=reason,
        reference=reference,
        actor=actor,
    )
