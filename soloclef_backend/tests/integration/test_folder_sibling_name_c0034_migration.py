"""c0034 on real Postgres: two sibling folders cannot share a name.

``create_folder``, ``update_folder`` and ``move_folder`` each catch
``uq_folder_space_parent_name`` and answer 409. The index did not exist:
migration 109 created it in raw SQL, every database since is built by
``Base.metadata.create_all`` and stamped (``alembic/env.py``), and the model
never declared it. So the three 409 branches were dead and duplicate names were
accepted -- while a folder's knowledge-store path is built from its name with
nothing to break a collision, so two siblings called "Reports" share one
``/documents/Reports`` path and a rename of either moves the other's git
subtree.

Replayed on a database of its own, for the reason c0031's and c0033's tests
are: the revision builds its index with ``CREATE INDEX CONCURRENTLY`` inside an
``autocommit_block``, which commits and cannot run inside someone else's
transaction.
"""

from __future__ import annotations

import importlib.util
import uuid
from pathlib import Path

import pytest
import pytest_asyncio
from alembic.operations import Operations
from alembic.runtime.migration import MigrationContext
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncEngine, create_async_engine
from sqlalchemy.pool import NullPool

from app.config import config as app_config
from app.db import Base
from tests.conftest import TEST_DATABASE_URL
from tests.integration.engine_connect_args import build_test_engine_connect_args

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[2]
    / "alembic"
    / "versions"
    / "c0034_folder_sibling_name_unique.py"
)

INDEX_NAME = "uq_folder_space_parent_name"


def _load():
    spec = importlib.util.spec_from_file_location("c0034", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


async def _step(engine: AsyncEngine, name: str):
    module = _load()

    def apply(sync_connection):
        context = MigrationContext.configure(sync_connection)
        module.op = Operations(context)
        with context.begin_transaction():
            getattr(module, name)()

    async with engine.connect() as conn:
        await conn.run_sync(apply)


def _connect_args(url: str) -> dict:
    return build_test_engine_connect_args(
        database_url=url,
        pgbouncer_transaction_pooling=app_config.DB_PGBOUNCER_TRANSACTION_POOLING,
    )


@pytest_asyncio.fixture
async def replay_engine():
    """A database of this test's own, built at head and dropped afterwards."""
    name = f"c0034_replay_{uuid.uuid4().hex[:10]}"
    admin = create_async_engine(
        TEST_DATABASE_URL,
        poolclass=NullPool,
        isolation_level="AUTOCOMMIT",
        connect_args=_connect_args(TEST_DATABASE_URL),
    )
    async with admin.connect() as conn:
        await conn.execute(text(f'CREATE DATABASE "{name}"'))

    url = TEST_DATABASE_URL.rsplit("/", 1)[0] + f"/{name}"
    engine = create_async_engine(
        url, poolclass=NullPool, connect_args=_connect_args(url)
    )
    try:
        async with engine.begin() as conn:
            await conn.execute(text("CREATE EXTENSION IF NOT EXISTS vector"))
            await conn.run_sync(Base.metadata.create_all)
        yield engine
    finally:
        await engine.dispose()
        async with admin.connect() as conn:
            await conn.execute(text(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)'))
        await admin.dispose()


@pytest_asyncio.fixture
async def pre_c0034(replay_engine: AsyncEngine) -> AsyncEngine:
    """Head, taken back to the shape this revision was written against.

    ``Base.metadata.create_all`` now declares the index -- which is the other
    half of the fix, since a fresh database never replays the chain -- so the
    replay has to undo it first.
    """
    async with replay_engine.begin() as conn:
        await conn.execute(text(f"DROP INDEX {INDEX_NAME}"))
    return replay_engine


async def _workspace(engine: AsyncEngine) -> int:
    owner = uuid.uuid4()
    async with engine.begin() as conn:
        await conn.execute(
            text(
                'INSERT INTO "user" (id, email, hashed_password, is_active, '
                "is_superuser, is_verified) VALUES (:id, :email, 'x', true, "
                "false, true)"
            ),
            {"id": owner, "email": f"{owner.hex[:8]}@danvics.test"},
        )
        return await conn.scalar(
            text(
                "INSERT INTO workspaces (name, citations_enabled, user_id, "
                "created_at) VALUES ('Space', true, :owner, now()) RETURNING id"
            ),
            {"owner": owner},
        )


async def _folder(
    engine: AsyncEngine,
    workspace_id: int,
    name: str,
    *,
    parent_id: int | None = None,
    position: str = "a0",
) -> int:
    async with engine.begin() as conn:
        return await conn.scalar(
            text(
                "INSERT INTO folders (name, position, parent_id, workspace_id, "
                "created_at, updated_at) VALUES (:name, :position, :parent, "
                ":workspace, now(), now()) RETURNING id"
            ),
            {
                "name": name,
                "position": position,
                "parent": parent_id,
                "workspace": workspace_id,
            },
        )


async def _index(engine: AsyncEngine) -> tuple[bool, str | None]:
    """Whether the index is there, and its definition."""
    async with engine.connect() as conn:
        definition = await conn.scalar(
            text("SELECT indexdef FROM pg_indexes WHERE indexname = :name"),
            {"name": INDEX_NAME},
        )
    return definition is not None, definition


async def _valid(engine: AsyncEngine) -> bool:
    async with engine.connect() as conn:
        return bool(
            await conn.scalar(
                text(
                    "SELECT indisvalid FROM pg_index JOIN pg_class "
                    "ON pg_class.oid = pg_index.indexrelid "
                    "WHERE pg_class.relname = :name"
                ),
                {"name": INDEX_NAME},
            )
        )


async def test_duplicate_sibling_names_are_accepted_without_the_index(pre_c0034):
    """The defect, stated: the 409 the routes catch could never happen."""
    workspace_id = await _workspace(pre_c0034)

    first = await _folder(pre_c0034, workspace_id, "Reports")
    second = await _folder(pre_c0034, workspace_id, "Reports", position="a1")

    assert first != second
    assert (await _index(pre_c0034))[0] is False


async def test_the_migration_builds_a_valid_unique_index(pre_c0034):
    workspace_id = await _workspace(pre_c0034)
    await _folder(pre_c0034, workspace_id, "Reports")

    await _step(pre_c0034, "upgrade")

    present, definition = await _index(pre_c0034)
    assert present
    assert await _valid(pre_c0034)
    assert "UNIQUE" in definition
    assert "COALESCE(parent_id, 0)" in definition


async def test_duplicates_at_the_root_are_refused_after_it(pre_c0034):
    """NULL parent_id is why the index is an expression and not a key."""
    workspace_id = await _workspace(pre_c0034)
    await _folder(pre_c0034, workspace_id, "Reports")
    await _step(pre_c0034, "upgrade")

    with pytest.raises(IntegrityError):
        await _folder(pre_c0034, workspace_id, "Reports", position="a1")


async def test_duplicates_under_one_parent_are_refused_after_it(pre_c0034):
    workspace_id = await _workspace(pre_c0034)
    parent = await _folder(pre_c0034, workspace_id, "Parent")
    await _folder(pre_c0034, workspace_id, "Reports", parent_id=parent)
    await _step(pre_c0034, "upgrade")

    with pytest.raises(IntegrityError):
        await _folder(
            pre_c0034, workspace_id, "Reports", parent_id=parent, position="a1"
        )


async def test_the_same_name_elsewhere_is_still_allowed(pre_c0034):
    first = await _workspace(pre_c0034)
    second = await _workspace(pre_c0034)
    parent = await _folder(pre_c0034, first, "Parent")
    await _step(pre_c0034, "upgrade")

    assert await _folder(pre_c0034, first, "Reports", parent_id=parent)
    assert await _folder(pre_c0034, first, "Reports", position="a1")
    assert await _folder(pre_c0034, second, "Reports")


async def test_existing_duplicates_stop_the_migration_and_name_them(pre_c0034):
    workspace_id = await _workspace(pre_c0034)
    await _folder(pre_c0034, workspace_id, "Reports")
    await _folder(pre_c0034, workspace_id, "Reports", position="a1")

    with pytest.raises(RuntimeError) as refused:
        await _step(pre_c0034, "upgrade")

    assert "Reports" in str(refused.value)
    assert f"workspace {workspace_id}" in str(refused.value)
    # Nothing half-built is left behind for every folder write to pay for.
    assert (await _index(pre_c0034))[0] is False


async def test_the_downgrade_removes_it(pre_c0034):
    workspace_id = await _workspace(pre_c0034)
    await _folder(pre_c0034, workspace_id, "Reports")
    await _step(pre_c0034, "upgrade")

    await _step(pre_c0034, "downgrade")

    assert (await _index(pre_c0034))[0] is False
    assert await _folder(pre_c0034, workspace_id, "Reports", position="a1")
