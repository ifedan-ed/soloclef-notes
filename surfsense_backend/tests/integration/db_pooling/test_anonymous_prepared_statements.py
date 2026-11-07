"""Against a real Postgres: the pooled connect args name no prepared statement.

``build_asyncpg_connect_args`` is unit-tested on the dict it returns, which
cannot show whether asyncpg obeys it. This runs it: statements are issued and
``pg_prepared_statements`` is read back inside the same transaction, so the
answer comes from the same server connection either way — a direct one or one
borrowed from pgbouncer.

A named statement here is the live production failure, not a tidiness point:
it outlives its transaction, so the next transaction can be handed a server
connection that never parsed it and the query dies with
``prepared statement "__asyncpg_stmt_2c6__" does not exist``, intermittently
and only under load.
"""

from __future__ import annotations

import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import create_async_engine
from sqlalchemy.pool import NullPool

from tests.conftest import TEST_DATABASE_URL
from tests.integration.engine_connect_args import build_test_engine_connect_args

pytestmark = pytest.mark.integration


async def _statement_names_left_behind(*, pgbouncer_transaction_pooling: bool):
    engine = create_async_engine(
        TEST_DATABASE_URL,
        poolclass=NullPool,
        connect_args=build_test_engine_connect_args(
            database_url=TEST_DATABASE_URL,
            pgbouncer_transaction_pooling=pgbouncer_transaction_pooling,
        ),
    )
    try:
        async with engine.begin() as conn:
            for value in range(3):
                await conn.execute(text(f"SELECT {value} AS probe"))
            result = await conn.execute(text("SELECT name FROM pg_prepared_statements"))
            return sorted(row[0] for row in result)
    finally:
        await engine.dispose()


async def test_pooled_connect_args_leave_nothing_named_on_the_connection():
    assert await _statement_names_left_behind(pgbouncer_transaction_pooling=True) == []


async def test_direct_connect_args_are_the_ones_that_name_statements():
    """Why the setting has to be asked for: asyncpg names them by default.

    If this ever returns nothing either, the test above has stopped proving
    anything and the pooled path needs a new probe.
    """
    assert await _statement_names_left_behind(pgbouncer_transaction_pooling=False) != []
