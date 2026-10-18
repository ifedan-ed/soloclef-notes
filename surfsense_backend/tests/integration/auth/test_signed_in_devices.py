"""The signed-in devices list keeps each sign-in's date through refreshes and the daily purge.

``purge_refresh_tokens`` (03:41 every day) deletes rotated rows once they are
past the rotation grace, so the family's first row is gone the next morning;
the sign-in time travels on every row instead (``signed_in_at``, c0027).
Real Postgres; ``refresh_tokens`` and the purge open their own sessions,
pointed here at the test's (rolled back after).
"""

from __future__ import annotations

from contextlib import asynccontextmanager
from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import select, update

from app.db import RefreshToken
from app.tasks.celery_tasks import refresh_token_cleanup_task as cleanup
from app.utils import refresh_tokens

pytestmark = pytest.mark.integration


@pytest.fixture
def same_transaction(monkeypatch, db_session):
    @asynccontextmanager
    async def this_transaction():
        yield db_session

    monkeypatch.setattr(refresh_tokens, "async_session_maker", this_transaction)
    monkeypatch.setattr(cleanup, "async_session_maker", this_transaction)

    # rotate_refresh_token opens a transaction of its own (session.begin());
    # inside the test's transaction that is a savepoint.
    real_begin = db_session.begin

    def nested_begin():
        return (
            db_session.begin_nested() if db_session.in_transaction() else real_begin()
        )

    monkeypatch.setattr(db_session, "begin", nested_begin)


@pytest.mark.asyncio
async def test_a_sign_in_is_dated_from_its_start_after_refreshes_and_the_purge(
    db_session, db_user, same_transaction, monkeypatch
):
    issued = await refresh_tokens.issue_refresh_token(
        db_user.id, kind="app", device_label="iPhone"
    )
    # The sign-in was 20 days ago (moved back, as time cannot be).
    began = datetime.now(UTC) - timedelta(days=20)
    await db_session.execute(
        update(RefreshToken)
        .where(RefreshToken.family_id == issued.family_id)
        .values(created_at=began, signed_in_at=began)
    )
    await db_session.flush()

    rotated = await refresh_tokens.rotate_refresh_token(issued.token)
    assert rotated is not None and rotated.refresh_token
    again = await refresh_tokens.rotate_refresh_token(rotated.refresh_token)
    assert again is not None and again.refresh_token

    monkeypatch.setattr(cleanup.config, "REFRESH_ROTATION_GRACE_SECONDS", 0)
    removed = await cleanup._purge_refresh_tokens()
    assert removed >= 2
    rows = (
        (
            await db_session.execute(
                select(RefreshToken).where(RefreshToken.family_id == issued.family_id)
            )
        )
        .scalars()
        .all()
    )
    assert len(rows) == 1, "only the live token is left"

    [device] = await refresh_tokens.list_devices(db_user.id)
    assert device.family_id == issued.family_id
    assert abs((device.signed_in_at - began).total_seconds()) < 1
    assert device.last_used_at > began + timedelta(days=19)
