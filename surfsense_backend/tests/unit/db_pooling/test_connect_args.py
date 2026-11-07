"""The asyncpg connect args differ for a direct connection and for pgbouncer.

Through pgbouncer in transaction pooling mode, two things that are right for a
direct connection are wrong: a named prepared statement (the next transaction
can land on another server connection, which fails with "prepared statement
... does not exist") and idle_in_transaction_session_timeout in the startup
packet (pgbouncer refuses the connection outright).
"""

from __future__ import annotations

import pytest

from app.db_pooling.connect_args import build_asyncpg_connect_args

pytestmark = pytest.mark.unit

_ASYNCPG_URL = "postgresql+asyncpg://user@host:5432/db"


def test_a_direct_connection_asks_postgres_to_reap_idle_transactions():
    connect_args = build_asyncpg_connect_args(
        database_url=_ASYNCPG_URL,
        idle_in_transaction_timeout_ms=900000,
        pgbouncer_transaction_pooling=False,
    )

    assert connect_args == {
        "server_settings": {"idle_in_transaction_session_timeout": "900000"}
    }


@pytest.mark.parametrize("idle_ms", [0, -1])
def test_a_disabled_idle_timeout_leaves_a_direct_connection_untouched(idle_ms):
    assert (
        build_asyncpg_connect_args(
            database_url=_ASYNCPG_URL,
            idle_in_transaction_timeout_ms=idle_ms,
            pgbouncer_transaction_pooling=False,
        )
        == {}
    )


def test_pgbouncer_gets_unnamed_prepared_statements_and_no_startup_parameter():
    connect_args = build_asyncpg_connect_args(
        database_url=_ASYNCPG_URL,
        idle_in_transaction_timeout_ms=900000,
        pgbouncer_transaction_pooling=True,
    )

    # pgbouncer answers an unsupported startup parameter by closing the
    # connection, so the timeout has to come from the database itself.
    assert "server_settings" not in connect_args
    # 0 on both caches: asyncpg's own and the one SQLAlchemy's dialect keeps.
    assert connect_args["statement_cache_size"] == 0
    assert connect_args["prepared_statement_cache_size"] == 0
    # A name of None is what makes asyncpg prepare anonymously, which is the
    # only statement that cannot outlive its transaction.
    assert connect_args["prepared_statement_name_func"]() is None


@pytest.mark.parametrize("database_url", [None, "", "postgresql://user@host/db"])
@pytest.mark.parametrize("pooling", [False, True])
def test_a_driver_that_is_not_asyncpg_takes_no_asyncpg_arguments(database_url, pooling):
    assert (
        build_asyncpg_connect_args(
            database_url=database_url,
            idle_in_transaction_timeout_ms=900000,
            pgbouncer_transaction_pooling=pooling,
        )
        == {}
    )
