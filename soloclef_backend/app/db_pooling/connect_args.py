"""asyncpg connect args, for a direct connection and for transaction pooling."""

from __future__ import annotations


def _unnamed_prepared_statement() -> None:
    """The statement name asyncpg should use: none at all.

    SQLAlchemy's asyncpg dialect prepares every statement it executes and
    passes this function's result as the name. ``None`` makes asyncpg use an
    anonymous prepared statement, which lives and dies inside the extended
    query it was parsed for. A named one outlives its transaction, and under
    transaction pooling the next transaction may be handed a different server
    connection — which is where ``prepared statement "__asyncpg_stmt_2c6__"
    does not exist`` comes from, intermittently and under load. Naming them
    uniquely (the recipe in SQLAlchemy's pgbouncer note) avoids the collision
    but leaves the statements behind on the server connections instead.
    """
    return None


def build_asyncpg_connect_args(
    *,
    database_url: str | None,
    idle_in_transaction_timeout_ms: int,
    pgbouncer_transaction_pooling: bool,
) -> dict:
    """Driver arguments for an engine on ``database_url``.

    Direct connection: ask Postgres to reap this session if it opens a
    transaction and goes idle, so an abandoned transaction cannot hold locks
    and wedge writes (the "FastAPI stuck at startup" failure).

    Through pgbouncer in transaction pooling mode: prepared statements have to
    be anonymous, and the timeout cannot be asked for in the startup packet —
    pgbouncer answers an unsupported startup parameter by closing the
    connection, and tracking it per client only works for parameters Postgres
    reports back, which this one is not. It is set on the database instead
    (``docker/postgresql.conf``), where it survives the pooler's DISCARD ALL.
    """
    if not database_url or "asyncpg" not in database_url:
        return {}

    if pgbouncer_transaction_pooling:
        return {
            # asyncpg's own implicit cache, and the one SQLAlchemy's dialect
            # keeps per connection: both would hand a statement to a server
            # connection that never parsed it.
            "statement_cache_size": 0,
            "prepared_statement_cache_size": 0,
            "prepared_statement_name_func": _unnamed_prepared_statement,
        }

    if idle_in_transaction_timeout_ms > 0:
        return {
            "server_settings": {
                "idle_in_transaction_session_timeout": str(
                    idle_in_transaction_timeout_ms
                )
            }
        }

    return {}
