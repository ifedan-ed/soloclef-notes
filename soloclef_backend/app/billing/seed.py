"""Put the default catalog in place when none exists, or restore it on request."""

from __future__ import annotations

from sqlalchemy import delete, select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession

from .defaults import seed_pack_rows, seed_plan_rows, seed_price_rows
from .persistence.models import BillingActionPrice, BillingPack, BillingPlan


async def ensure_catalog(session: AsyncSession) -> None:
    """Seed plans, prices and packs that are missing (by slug / action)."""
    await session.execute(
        pg_insert(BillingPlan)
        .values(seed_plan_rows())
        .on_conflict_do_nothing(index_elements=["slug"])
    )
    await session.execute(
        pg_insert(BillingActionPrice)
        .values(seed_price_rows())
        .on_conflict_do_nothing(index_elements=["action"])
    )
    if await session.scalar(select(BillingPack.id).limit(1)) is None:
        await session.execute(
            pg_insert(BillingPack)
            .values(seed_pack_rows())
            .on_conflict_do_nothing(index_elements=["slug"])
        )
    await session.flush()


async def reset_prices_and_packs(session: AsyncSession) -> None:
    """Admin "reset to defaults" for per-action prices and packs.

    Plans are not reset wholesale, because accounts point at them; an admin
    edits them back by hand.
    """
    for row in seed_price_rows():
        await session.execute(
            pg_insert(BillingActionPrice)
            .values(row)
            .on_conflict_do_update(
                index_elements=["action"],
                set_={
                    "micros": row["micros"],
                    "unit": row["unit"],
                    "content": row["content"],
                },
            )
        )
    await session.execute(delete(BillingPack))
    await session.execute(pg_insert(BillingPack).values(seed_pack_rows()))
    await session.flush()
