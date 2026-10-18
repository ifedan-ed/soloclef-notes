"""c0029 on real Postgres: connector config leaves the Zero publication, thread access joins it.

The test schema is built from the models, with no publication; one is made
inside the test transaction (as a fresh deploy's ``ensure_publication`` would),
then the revision's upgrade and downgrade run against it. Rolls back.
"""

from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest
from alembic.operations import Operations
from alembic.runtime.migration import MigrationContext
from sqlalchemy import text

from app.zero_publication import PUBLICATION_NAME, verify_publication

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[2]
    / "alembic"
    / "versions"
    / "c0029_zero_config_thread_access.py"
)


def _load():
    spec = importlib.util.spec_from_file_location("c0029", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _run(session, step: str):
    module = _load()

    def apply(sync_session):
        context = MigrationContext.configure(sync_session.connection())
        module.op = Operations(context)
        getattr(module, step)()

    return session.run_sync(apply)


async def _published(session, table: str) -> list[str] | None:
    row = (
        await session.execute(
            text(
                "SELECT pr.prattrs IS NULL AS all_columns, pt.attnames "
                "FROM pg_publication_tables pt "
                "JOIN pg_publication p ON p.pubname = pt.pubname "
                "JOIN pg_class c ON c.relname = pt.tablename "
                "JOIN pg_namespace n ON n.oid = c.relnamespace "
                "AND n.nspname = pt.schemaname "
                "JOIN pg_publication_rel pr ON pr.prpubid = p.oid "
                "AND pr.prrelid = c.oid "
                "WHERE pt.pubname = :name AND pt.tablename = :table "
                "AND pt.schemaname = current_schema()"
            ),
            {"name": PUBLICATION_NAME, "table": table},
        )
    ).first()
    assert row is not None, f"{table} is not in the publication"
    return None if row.all_columns else sorted(row.attnames)


async def test_upgrade_drops_config_and_publishes_thread_access(db_session):
    exists = await db_session.scalar(
        text("SELECT 1 FROM pg_publication WHERE pubname = :name"),
        {"name": PUBLICATION_NAME},
    )
    if not exists:
        await db_session.execute(
            text(f'CREATE PUBLICATION "{PUBLICATION_NAME}" FOR TABLE notifications')
        )

    await _run(db_session, "upgrade")
    connectors = await _published(db_session, "search_source_connectors")
    assert connectors is not None and "config" not in connectors
    assert {"id", "name", "workspace_id", "user_id"} <= set(connectors)
    threads = await _published(db_session, "new_chat_threads")
    assert threads == sorted(["id", "workspace_id", "visibility", "created_by_id"])
    assert await db_session.run_sync(lambda s: verify_publication(s.connection())) == []

    await _run(db_session, "downgrade")
    assert await _published(db_session, "search_source_connectors") is None
    assert await _published(db_session, "new_chat_threads") == ["id", "workspace_id"]
