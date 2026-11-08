"""Driver arguments for the integration suite's own engine.

The suite has to connect the way the app connects, or a run against pgbouncer
in transaction pooling mode proves nothing: asyncpg prepares every statement it
executes, and left to itself it names them (``__asyncpg_stmt_8__``). A named
statement outlives its transaction, so under transaction pooling the next
transaction can be handed a server connection that never parsed it — the
intermittent ``prepared statement ... does not exist`` the app's own
``build_asyncpg_connect_args`` exists to prevent.

On top of that the suite needs one setting of its own, which production does
not: the fixtures nest savepoints inside one outer transaction per test, and
asyncpg's per-connection statement cache makes a savepoint rollback fail with
"another operation is in progress".
"""

from __future__ import annotations

from app.db_pooling.connect_args import build_asyncpg_connect_args

_SAVEPOINT_SAFE = {"prepared_statement_cache_size": 0}


def build_test_engine_connect_args(
    *, database_url: str, pgbouncer_transaction_pooling: bool
) -> dict:
    """What the suite's engine passes to asyncpg for ``database_url``."""
    return _SAVEPOINT_SAFE | build_asyncpg_connect_args(
        database_url=database_url,
        # The suite's transactions are short-lived and bounded by the test, so
        # it does not ask Postgres for the app's idle-transaction reaper.
        idle_in_transaction_timeout_ms=0,
        pgbouncer_transaction_pooling=pgbouncer_transaction_pooling,
    )
