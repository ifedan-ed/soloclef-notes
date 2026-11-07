"""The checkpointer's own pool has to survive a transaction pooler.

It is a psycopg3 pool, separate from the app's asyncpg engine, and it is the one
that broke the backend when pgbouncer went in front of Postgres: psycopg
prepares a statement once it has run `prepare_threshold` times, so 0 prepares
everything immediately and a prepared statement cannot outlive its transaction
under transaction pooling. Only None disables preparation.
"""

import inspect

import pytest

from app.agents.chat.runtime import checkpointer


@pytest.mark.unit
def test_prepared_statements_are_disabled_not_merely_eager():
    source = inspect.getsource(checkpointer._create_checkpointer)
    assert '"prepare_threshold": None' in source, (
        "prepare_threshold must be None. 0 means 'prepare on first use', which "
        'fails behind a transaction pooler with `prepared statement "_pg3_0" '
        "does not exist`."
    )
    assert '"prepare_threshold": 0' not in source


@pytest.mark.unit
def test_setup_still_gets_the_autocommit_it_needs():
    source = inspect.getsource(checkpointer._create_checkpointer)
    assert '"autocommit": True' in source, (
        "AsyncPostgresSaver.setup() commits its checkpoint tables on the "
        "connection, so it cannot run inside an open transaction."
    )


@pytest.mark.unit
@pytest.mark.parametrize(
    ("url", "expected"),
    [
        (
            "postgresql+asyncpg://u:p@host:5432/db",
            "postgresql://u:p@host:5432/db",
        ),
        (
            "postgresql+asyncpg://u:p@soloclef-pgbouncer-1:6432/db",
            "postgresql://u:p@soloclef-pgbouncer-1:6432/db",
        ),
        ("postgresql://u:p@host:5432/db", "postgresql://u:p@host:5432/db"),
    ],
)
def test_the_pool_follows_whichever_host_database_url_names(monkeypatch, url, expected):
    """Pooled or direct, the checkpointer goes wherever DATABASE_URL points."""
    monkeypatch.setattr(checkpointer.config, "DATABASE_URL", url)
    assert checkpointer.get_postgres_connection_string() == expected
