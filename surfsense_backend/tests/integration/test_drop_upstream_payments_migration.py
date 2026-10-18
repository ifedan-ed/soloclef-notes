"""c0011 drops upstream's purchase, auto-reload and incentive storage, and
its downgrade puts the structure back.

The test schema is built from the models, which no longer have these tables
or columns, so the downgrade runs first (recreating them as upstream left
them), then the upgrade removes them again; everything billing keeps on the
user must survive. Runs on the test transaction and rolls back.
"""

from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest
from alembic.operations import Operations
from alembic.runtime.migration import MigrationContext
from sqlalchemy import text

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[2]
    / "alembic"
    / "versions"
    / "c0011_drop_upstream_payments.py"
)
_TABLES = ("credit_purchases", "page_purchases", "user_incentive_tasks")
_ENUMS = ("creditpurchasestatus", "pagepurchasestatus", "incentivetasktype")


def _load():
    spec = importlib.util.spec_from_file_location("c0011", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _run(session, step: str) -> None:
    module = _load()

    def apply(sync_session):
        context = MigrationContext.configure(sync_session.connection())
        module.op = Operations(context)
        getattr(module, step)()

    return session.run_sync(apply)


async def _state(session) -> dict:
    tables = set(
        (
            await session.execute(
                text(
                    "select table_name from information_schema.tables "
                    "where table_schema = current_schema() and table_name = any(:t)"
                ),
                {"t": list(_TABLES)},
            )
        ).scalars()
    )
    enums = set(
        (
            await session.execute(
                text("select typname from pg_type where typname = any(:e)"),
                {"e": list(_ENUMS)},
            )
        ).scalars()
    )
    user_columns = set(
        (
            await session.execute(
                text(
                    "select column_name from information_schema.columns "
                    "where table_schema = current_schema() and table_name = 'user'"
                )
            )
        ).scalars()
    )
    return {"tables": tables, "enums": enums, "user_columns": user_columns}


async def test_the_downgrade_restores_and_the_upgrade_drops(db_session, db_user):
    db_user.stripe_customer_id = "cus_keep"
    db_user.credit_micros_balance = 123
    await db_session.flush()

    await _run(db_session, "downgrade")
    restored = await _state(db_session)
    assert restored["tables"] == set(_TABLES)
    assert restored["enums"] == set(_ENUMS)
    auto_reload = {c for c in restored["user_columns"] if c.startswith("auto_reload_")}
    assert auto_reload == {
        "auto_reload_enabled",
        "auto_reload_threshold_micros",
        "auto_reload_amount_micros",
        "auto_reload_payment_method_id",
        "auto_reload_failed_at",
    }
    # The recreated table takes a row the way upstream wrote it.
    await db_session.execute(
        text(
            "insert into user_incentive_tasks "
            "(user_id, task_type, credit_micros_awarded, completed_at, created_at) "
            "values (:u, 'GITHUB_STAR', 30000, now(), now())"
        ),
        {"u": db_user.id},
    )

    await _run(db_session, "upgrade")
    dropped = await _state(db_session)
    assert dropped["tables"] == set()
    assert dropped["enums"] == set()
    assert not any(c.startswith("auto_reload_") for c in dropped["user_columns"])
    # What billing uses stays.
    assert {
        "stripe_customer_id",
        "credit_micros_balance",
        "credit_micros_reserved",
    } <= (dropped["user_columns"])
    kept = (
        await db_session.execute(
            text(
                'select stripe_customer_id, credit_micros_balance from "user" '
                "where id = :u"
            ),
            {"u": db_user.id},
        )
    ).one()
    assert tuple(kept) == ("cus_keep", 123)

    # A second upgrade (already applied elsewhere) is a no-op, not an error.
    await _run(db_session, "upgrade")
