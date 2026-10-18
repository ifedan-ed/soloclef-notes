"""Two holds at once cannot spend the same credit (real concurrent sessions)."""

from __future__ import annotations

import asyncio
import uuid

import pytest
from sqlalchemy import delete, text
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.billing import ledger, meter
from app.billing.errors import BillingDenied
from app.billing.persistence.models import BillingSetting
from app.billing.seed import ensure_catalog
from app.db import User

pytestmark = pytest.mark.integration


@pytest.fixture(autouse=True)
def billing_db():
    """Override the transactional fixture: this test needs committed rows."""
    return None


async def test_parallel_reserves_cannot_overspend(async_engine):
    factory = async_sessionmaker(async_engine, expire_on_commit=False)
    user_id = uuid.uuid4()
    async with factory() as session:
        await ensure_catalog(session)
        session.add(BillingSetting(key="mode", value="live"))
        session.add(
            User(
                id=user_id,
                email=f"{user_id.hex[:8]}@race.net",
                hashed_password="x",
                is_active=True,
                is_superuser=False,
                is_verified=True,
            )
        )
        await session.commit()
        account, user = await ledger.lock_account(session, user_id)
        account.plan_credits_micros = 250_000  # 25 credits: one report (20), not two
        await session.commit()
    try:
        results = await asyncio.gather(
            *(
                meter.begin(user_id, "studio_report", session_factory=factory)
                for _ in range(6)
            ),
            return_exceptions=True,
        )
        granted = [r for r in results if isinstance(r, meter.Charge)]
        denied = [r for r in results if isinstance(r, BillingDenied)]
        assert len(granted) == 1
        assert len(denied) == 5
        async with factory() as session:
            account, user = await ledger.lock_account(session, user_id)
            assert ledger.balances(account, user).spendable_micros == 50_000
            await session.commit()
    finally:
        async with factory() as session:
            await session.execute(
                text("DELETE FROM billing_usage WHERE user_id = :u"), {"u": user_id}
            )
            await session.execute(
                text("DELETE FROM billing_ledger WHERE user_id = :u"), {"u": user_id}
            )
            await session.execute(
                text("DELETE FROM billing_accounts WHERE user_id = :u"), {"u": user_id}
            )
            await session.execute(delete(User).where(User.id == user_id))
            await session.execute(delete(BillingSetting))
            await session.execute(text("DELETE FROM billing_packs"))
            await session.execute(text("DELETE FROM billing_action_prices"))
            await session.execute(text("DELETE FROM billing_plans"))
            await session.commit()
