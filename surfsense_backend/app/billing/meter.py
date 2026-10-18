"""Meter an action: check the plan, hold its price, then settle or give it back.

The lifecycle every gated action follows::

    charge = await begin(user_id, "studio_report", workspace_id=..., need=Requirement(studio_kind="report"))
    ...run the action...
    await settle(charge)            # success: the hold becomes a debit
    await release(charge)           # failure before any charge: the hold is dropped
    await refund(charge)            # failure after settling: the debit is returned

or, for work that finishes inside one request, ``async with metered(...)``.

Long jobs pass a ``reference`` (``"deliverable_job:42"``) so the worker that
finishes them can settle or release by reference without the handle, and so a
retried start never holds twice.

Modes (admin setting ``mode``):

* ``off``    — nothing is recorded or checked; every call is a no-op.
* ``shadow`` — every action is recorded in ``billing_usage`` with the credits it
  would have cost and, if live mode would have refused it, why
  (``denial_code``). Nothing is refused or debited.
* ``live``   — plan rules and balances are enforced; :class:`BillingDenied` is
  raised before the action runs.

Exempt accounts (the owner, chosen admins) are recorded with ``mode="exempt"``
and never refused or debited, whatever the mode.

**Workspace billing.** When a call carries a ``workspace_id``, the payer is
resolved to that workspace's owner (``Workspace.user_id``; ownership never
transfers today) rather than the acting user: an invited member's usage
inside someone else's workspace spends *that owner's* credits, like a
registered member using the owner's plan. Every entitlement and balance
check runs against the payer's account, and the payer's own exempt status
governs — if the owner is exempt, a member's usage there is still recorded
(for the "usage by member" view) but never debited, same as the owner's own
usage. Outside a workspace, or when the acting user is the workspace's own
owner, the payer is the acting user, unchanged from before. A usage row
always carries both ``user_id`` (who did it) and ``payer_user_id`` (who paid
for it), fixed at ``begin``/``record`` time so a membership change mid-job
never moves an open hold to someone else.

Each call runs in its own session and commits, so a caller's rollback can never
undo a debit and a debit can never flush the caller's half-done rows.
"""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass, replace
from typing import Any
from uuid import UUID

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from . import ledger
from .entitlements import INSUFFICIENT_CREDITS, Requirement, denial
from .errors import BillingDenied
from .ledger import now_utc
from .persistence.models import BillingActionPrice, BillingPlan, BillingUsage
from .settings_store import load_settings

logger = logging.getLogger(__name__)

SessionFactory = Callable[[], AbstractAsyncContextManager[AsyncSession]]
STUDIO_PREFIX = "studio_"


def _default_factory() -> SessionFactory:
    from app.db import shielded_async_session

    return shielded_async_session


@dataclass(frozen=True)
class Charge:
    """A handle on one metered action. ``usage_id`` is ``None`` when off."""

    usage_id: int | None
    mode: str
    action: str
    micros: int = 0

    @property
    def enforced(self) -> bool:
        return self.mode == "live"


OFF = Charge(usage_id=None, mode="off", action="")


async def action_price(session: AsyncSession, action: str) -> int:
    row = await session.get(BillingActionPrice, action)
    return int(row.micros) if row is not None else 0


async def _running_jobs(session: AsyncSession, payer_id: UUID) -> int:
    """Studio jobs open against ``payer_id``'s plan, whoever started them."""
    return int(
        await session.scalar(
            select(func.count(BillingUsage.id)).where(
                BillingUsage.payer_user_id == payer_id,
                BillingUsage.status == "reserved",
                BillingUsage.action.startswith(STUDIO_PREFIX),
            )
        )
        or 0
    )


async def _workspace_owner(
    session: AsyncSession, workspace_id: int | None
) -> UUID | None:
    if workspace_id is None:
        return None
    from app.db import Workspace

    return await session.scalar(
        select(Workspace.user_id).where(Workspace.id == workspace_id)
    )


async def _resolve_payer(
    session: AsyncSession, user_id: UUID, workspace_id: int | None
) -> UUID:
    """Who actually pays: the workspace's owner, or the acting user.

    Ownership never transfers today (see ``app/workspace_roles``), so
    ``Workspace.user_id`` is always the Owner. A deleted or unknown workspace
    falls back to the acting user rather than failing the whole action.
    """
    owner_id = await _workspace_owner(session, workspace_id)
    return owner_id or user_id


async def _existing(
    session: AsyncSession, reference: str | None
) -> BillingUsage | None:
    if reference is None:
        return None
    return await session.scalar(
        select(BillingUsage).where(BillingUsage.reference == reference)
    )


async def begin(
    user_id: UUID | str,
    action: str,
    *,
    units: int = 1,
    need: Requirement | None = None,
    workspace_id: int | None = None,
    notebook_id: int | None = None,
    thread_id: int | None = None,
    reference: str | None = None,
    details: dict[str, Any] | None = None,
    session_factory: SessionFactory | None = None,
) -> Charge:
    """Check the plan and hold the action's price. Raises :class:`BillingDenied`
    in live mode when the plan does not include it or credits are short.
    """
    user_id = UUID(str(user_id))
    factory = session_factory or _default_factory()
    units = max(0, int(units))
    async with factory() as session:
        settings = await load_settings(session)
        mode = settings.mode
        if mode == "off":
            return OFF
        payer_id = await _resolve_payer(session, user_id, workspace_id)
        account, user = await ledger.lock_account(session, payer_id)
        existing = await _existing(session, reference)
        if existing is not None:
            await session.commit()
            return Charge(
                existing.id, existing.mode, existing.action, existing.reserved_micros
            )

        plan = await session.get(BillingPlan, account.plan_id)
        price = await action_price(session, action) * units
        requirement = need or Requirement()
        if action.startswith(STUDIO_PREFIX):
            requirement = replace(
                requirement, running_jobs=await _running_jobs(session, payer_id)
            )
        code = denial(plan.entitlements if plan else None, requirement)
        balances = ledger.balances(account, user)
        if code is None and price > balances.spendable_micros:
            code = INSUFFICIENT_CREDITS
        exempt = account.exempt or ledger.is_config_admin(user)
        effective = "exempt" if exempt else mode

        usage = BillingUsage(
            user_id=user_id,
            payer_user_id=payer_id,
            workspace_id=workspace_id,
            notebook_id=notebook_id,
            thread_id=thread_id,
            action=action,
            units=units,
            unit_micros=price // units if units else 0,
            reserved_micros=price,
            charged_micros=0,
            mode=effective,
            status="reserved",
            denial_code=code,
            reference=reference,
            details=details or {},
            created_at=now_utc(),
        )
        if effective == "live" and code is not None:
            usage.status = "denied"
            usage.reserved_micros = 0
            usage.reference = None
            session.add(usage)
            await session.commit()
            logger.info(
                "[billing] denied user=%s action=%s code=%s price=%d spendable=%d",
                user_id,
                action,
                code,
                price,
                balances.spendable_micros,
            )
            raise BillingDenied(
                code,
                action=action,
                required_micros=price,
                available_micros=balances.spendable_micros,
            )
        session.add(usage)
        if effective == "live":
            account.reserved_micros = int(account.reserved_micros) + price
        await session.flush()
        usage_id = usage.id
        await session.commit()
        return Charge(usage_id, effective, action, price)


async def _find_usage(
    session: AsyncSession, charge: Charge | str
) -> BillingUsage | None:
    """Unlocked read: the account is locked first (user, account, usage is the
    lock order everywhere), then the row is re-read under ``_relock``."""
    query = select(BillingUsage)
    if isinstance(charge, Charge):
        if charge.usage_id is None:
            return None
        query = query.where(BillingUsage.id == charge.usage_id)
    else:
        query = query.where(BillingUsage.reference == charge)
    return await session.scalar(query)


async def _relock(session: AsyncSession, usage_id: int) -> BillingUsage:
    return await session.scalar(
        select(BillingUsage)
        .where(BillingUsage.id == usage_id)
        .with_for_update()
        .execution_options(populate_existing=True)
    )


async def settle(
    charge: Charge | str | None,
    *,
    units: int | None = None,
    details: dict[str, Any] | None = None,
    session_factory: SessionFactory | None = None,
) -> int:
    """Turn the hold into a debit. ``units`` re-prices by the actual count
    (pages parsed). Idempotent. Returns the micros charged (would-be in shadow).
    """
    if charge is None or (isinstance(charge, Charge) and charge.usage_id is None):
        return 0
    factory = session_factory or _default_factory()
    async with factory() as session:
        usage = await _find_usage(session, charge)
        if usage is None or usage.status != "reserved":
            return int(usage.charged_micros) if usage is not None else 0
        account, user = await ledger.lock_account(
            session, usage.payer_user_id, roll=False
        )
        usage = await _relock(session, usage.id)
        if usage.status != "reserved":
            await session.commit()
            return int(usage.charged_micros)
        amount = int(usage.reserved_micros)
        if units is not None:
            usage.units = max(0, int(units))
            amount = int(usage.unit_micros) * usage.units
        if usage.mode == "live":
            account.reserved_micros = max(
                0, int(account.reserved_micros) - int(usage.reserved_micros)
            )
            await ledger.debit(
                session,
                account,
                user,
                amount,
                reference=f"usage:{usage.id}",
                usage_id=usage.id,
                reason=usage.action,
            )
        usage.charged_micros = amount
        usage.status = "settled"
        usage.settled_at = now_utc()
        if details:
            usage.details = {**(usage.details or {}), **details}
        await session.commit()
        return amount


async def release(
    charge: Charge | str | None,
    *,
    reason: str | None = None,
    session_factory: SessionFactory | None = None,
) -> None:
    """The action failed: drop its hold, or refund it if it was already settled."""
    if charge is None or (isinstance(charge, Charge) and charge.usage_id is None):
        return
    factory = session_factory or _default_factory()
    async with factory() as session:
        usage = await _find_usage(session, charge)
        if usage is None or usage.status not in {"reserved", "settled"}:
            return
        account, user = await ledger.lock_account(
            session, usage.payer_user_id, roll=False
        )
        usage = await _relock(session, usage.id)
        if usage.status == "reserved":
            if usage.mode == "live":
                account.reserved_micros = max(
                    0, int(account.reserved_micros) - int(usage.reserved_micros)
                )
            usage.status = "released"
        elif usage.status == "settled":
            if usage.mode == "live" and usage.charged_micros:
                await _refund_debits(session, account, user, usage)
            usage.status = "refunded"
        else:
            return
        usage.settled_at = now_utc()
        if reason:
            usage.details = {**(usage.details or {}), "release_reason": reason[:300]}
        if usage.reference:
            # Free the name: a retried job holds again under the same one.
            usage.details = {**(usage.details or {}), "reference": usage.reference}
            usage.reference = None
        await session.commit()


refund = release


async def _refund_debits(
    session: AsyncSession, account, user, usage: BillingUsage
) -> None:
    """Return each debit to the bucket it came from.

    Plan credits come back only while their period is still running; after
    that they would have expired, so they are returned as top-up instead.
    """
    from .persistence.models import BillingLedgerEntry

    entries = (
        (
            await session.execute(
                select(BillingLedgerEntry).where(
                    BillingLedgerEntry.usage_id == usage.id,
                    BillingLedgerEntry.kind == "debit",
                )
            )
        )
        .scalars()
        .all()
    )
    for entry in entries:
        bucket = entry.bucket
        if bucket == ledger.PLAN and entry.created_at < account.period_start:
            bucket = ledger.TOPUP
        await ledger.post(
            session,
            account,
            user,
            bucket=bucket,
            delta=-int(entry.delta_micros),
            kind="refund",
            reason=f"Refund: {usage.action}",
            reference=f"refund:{entry.id}",
            usage_id=usage.id,
        )


@asynccontextmanager
async def metered(
    user_id: UUID | str, action: str, **kwargs: Any
) -> AsyncIterator[Charge]:
    """``begin`` → body → ``settle``; ``release`` if the body raises."""
    factory = kwargs.get("session_factory")
    charge = await begin(user_id, action, **kwargs)
    try:
        yield charge
    except BaseException as exc:
        try:
            await release(charge, reason=type(exc).__name__, session_factory=factory)
        except Exception:
            logger.exception("[billing] release failed for %s", charge)
        raise
    await settle(charge, session_factory=factory)


@dataclass(frozen=True)
class Decision:
    """A dry run of ``begin``: nothing is written."""

    mode: str
    code: str | None
    price_micros: int
    spendable_micros: int

    @property
    def refused(self) -> bool:
        return self.mode == "live" and self.code is not None


async def check(
    user_id: UUID | str,
    action: str,
    *,
    units: int = 1,
    need: Requirement | None = None,
    workspace_id: int | None = None,
    raise_if_refused: bool = True,
    session_factory: SessionFactory | None = None,
) -> Decision:
    """Would ``begin`` allow this? Raises :class:`BillingDenied` when live
    billing would refuse it (unless ``raise_if_refused`` is off).

    With a ``workspace_id``, checked against that workspace's owner (the
    would-be payer), same as ``begin``, so a pre-flight check (e.g. before
    parsing an upload) sees the same plan and balance ``begin`` will."""
    user_id = UUID(str(user_id))
    factory = session_factory or _default_factory()
    async with factory() as session:
        settings = await load_settings(session)
        if settings.mode == "off":
            return Decision("off", None, 0, 0)
        payer_id = await _resolve_payer(session, user_id, workspace_id)
        account, user = await ledger.lock_account(session, payer_id)
        plan = await session.get(BillingPlan, account.plan_id)
        price = await action_price(session, action) * max(0, int(units))
        code = denial(plan.entitlements if plan else None, need or Requirement())
        spendable = ledger.balances(account, user).spendable_micros
        if code is None and price > spendable:
            code = INSUFFICIENT_CREDITS
        exempt = account.exempt or ledger.is_config_admin(user)
        await session.commit()
    decision = Decision("exempt" if exempt else settings.mode, code, price, spendable)
    if decision.refused and raise_if_refused:
        raise BillingDenied(
            code, action=action, required_micros=price, available_micros=spendable
        )
    return decision


async def record(
    user_id: UUID | str,
    action: str,
    *,
    units: int = 1,
    need: Requirement | None = None,
    workspace_id: int | None = None,
    notebook_id: int | None = None,
    thread_id: int | None = None,
    reference: str | None = None,
    details: dict[str, Any] | None = None,
    session_factory: SessionFactory | None = None,
) -> int:
    """Charge for work already done (pages parsed). Never refuses: the check
    happened before the work; a short balance lets top-up dip below zero.
    Returns micros charged (would-be in shadow)."""
    user_id = UUID(str(user_id))
    factory = session_factory or _default_factory()
    async with factory() as session:
        settings = await load_settings(session)
        if settings.mode == "off":
            return 0
        payer_id = await _resolve_payer(session, user_id, workspace_id)
        account, user = await ledger.lock_account(session, payer_id)
        if await _existing(session, reference) is not None:
            await session.commit()
            return 0
        plan = await session.get(BillingPlan, account.plan_id)
        units = max(0, int(units))
        unit_price = await action_price(session, action)
        price = unit_price * units
        code = denial(plan.entitlements if plan else None, need or Requirement())
        if code is None and price > ledger.balances(account, user).spendable_micros:
            code = INSUFFICIENT_CREDITS
        exempt = account.exempt or ledger.is_config_admin(user)
        mode = "exempt" if exempt else settings.mode
        usage = BillingUsage(
            user_id=user_id,
            payer_user_id=payer_id,
            workspace_id=workspace_id,
            notebook_id=notebook_id,
            thread_id=thread_id,
            action=action,
            units=units,
            unit_micros=unit_price,
            reserved_micros=0,
            charged_micros=price,
            mode=mode,
            status="settled",
            denial_code=code,
            reference=reference,
            details=details or {},
            created_at=now_utc(),
            settled_at=now_utc(),
        )
        session.add(usage)
        await session.flush()
        if mode == "live":
            await ledger.debit(
                session,
                account,
                user,
                price,
                reference=f"usage:{usage.id}",
                usage_id=usage.id,
                reason=action,
            )
        await session.commit()
        return price


async def attach_reference(
    charge: Charge | None,
    reference: str,
    *,
    session_factory: SessionFactory | None = None,
) -> None:
    """Name a hold after the job it paid for, so the job's end can settle it."""
    if charge is None or charge.usage_id is None:
        return
    factory = session_factory or _default_factory()
    async with factory() as session:
        usage = await session.get(BillingUsage, charge.usage_id)
        if usage is not None:
            usage.reference = reference
            await session.commit()


def sibling_sessions(session: AsyncSession) -> SessionFactory:
    """A factory for fresh sessions on ``session``'s engine or connection.

    Workers pass this so billing never shares (or commits) the caller's
    transaction but stays on the worker's own event loop.
    """
    if not isinstance(session, AsyncSession):
        # A stand-in session (tests): billing uses its own.
        return _default_factory()
    bind = session.bind

    @asynccontextmanager
    async def factory():
        fresh = AsyncSession(
            bind=bind, expire_on_commit=False, join_transaction_mode="create_savepoint"
        )
        try:
            yield fresh
        finally:
            await fresh.close()

    return factory
