"""c0028 adds site_announcements.cleared_read_count, 0 on existing rows, and takes it away again."""

from __future__ import annotations

import importlib.util
from datetime import UTC, datetime
from pathlib import Path

import pytest
from alembic.operations import Operations
from alembic.runtime.migration import MigrationContext
from sqlalchemy import inspect, text

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[3]
    / "alembic"
    / "versions"
    / "c0028_announcement_cleared_reads.py"
)


def _revision():
    spec = importlib.util.spec_from_file_location("c0028", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _run(sync_connection, step: str) -> None:
    module = _revision()
    module.op = Operations(MigrationContext.configure(sync_connection))
    getattr(module, step)()


def _columns(sync_connection) -> set[str]:
    return {
        c["name"] for c in inspect(sync_connection).get_columns("site_announcements")
    }


async def test_upgrade_adds_the_count_at_zero_and_downgrade_removes_it(db_session):
    connection = await db_session.connection()
    await connection.execute(
        text("ALTER TABLE site_announcements DROP COLUMN cleared_read_count")
    )
    await connection.execute(
        text(
            "INSERT INTO site_announcements (title, body, audience, recipient_count, created_at) "
            "VALUES ('Before', 'Sent before c0028', 'all', 2, :now)"
        ),
        {"now": datetime.now(UTC)},
    )

    await connection.run_sync(_run, "upgrade")

    assert "cleared_read_count" in await connection.run_sync(_columns)
    assert (
        await connection.scalar(
            text(
                "SELECT cleared_read_count FROM site_announcements WHERE title = 'Before'"
            )
        )
        == 0
    )

    await connection.run_sync(_run, "downgrade")
    assert "cleared_read_count" not in await connection.run_sync(_columns)
