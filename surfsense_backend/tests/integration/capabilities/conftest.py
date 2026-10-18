"""Billing fixtures on the transactional test database (mirrors
``tests/integration/billing/conftest.py``'s pattern for this folder: the
capability billing module opens its own sessions, so they must be siblings
of the test's ``db_session`` to see its uncommitted rows and roll back with
it).
"""

from __future__ import annotations

import pytest
from sqlalchemy.ext.asyncio import AsyncSession

from app.billing import meter
from app.billing.seed import ensure_catalog
from app.billing.settings_store import save_settings


@pytest.fixture(autouse=True)
async def billing_db(db_session: AsyncSession, monkeypatch):
    await ensure_catalog(db_session)
    monkeypatch.setattr(
        meter, "_default_factory", lambda: meter.sibling_sessions(db_session)
    )
    return db_session


@pytest.fixture
def set_mode(db_session: AsyncSession):
    async def apply(mode: str, **values):
        await save_settings(db_session, values={"mode": mode, **values})

    return apply
