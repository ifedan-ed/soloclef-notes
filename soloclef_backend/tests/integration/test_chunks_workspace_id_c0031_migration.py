"""c0031 on real Postgres: chunks get their own workspace_id, backfilled and indexed.

``alembic/env.py`` fast-forwards an empty database with
``Base.metadata.create_all`` and stamps head, so this revision's SQL has never
run in a test -- only once, in production.

Why this file cannot use ``db_session`` the way the c0030 and c0032 replays do
------------------------------------------------------------------------------
Those tests run a revision inside the one transaction the session fixture
rolls back, which is what keeps them from touching the shared test schema.
c0031 cannot: it backfills and builds its index inside an
``op.get_context().autocommit_block()``, which commits the migration's
transaction and switches the connection to AUTOCOMMIT -- ``CREATE INDEX
CONCURRENTLY`` is rejected inside a transaction block at all. Handed a
connection that is already in someone else's transaction, alembic asserts
(``assert self._transaction is not None``) and the replay never starts.

So the revision needs a connection it owns and commits on, which means its
DDL lands for real. Each test therefore gets its own throwaway *database*:
a committed ``DROP COLUMN workspace_id`` against the session-wide schema
would outlive a failed assertion and break every test after it.
"""

from __future__ import annotations

import importlib.util
import uuid
from datetime import UTC, datetime
from pathlib import Path

import pytest
import pytest_asyncio
from alembic.operations import Operations
from alembic.runtime.migration import MigrationContext
from sqlalchemy import text
from sqlalchemy.exc import DBAPIError
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
    / "c0031_chunks_workspace_id.py"
)

INDEX_NAME = "ix_chunks_workspace_id"
FK_NAME = "chunks_workspace_id_fkey"


def _load():
    spec = importlib.util.spec_from_file_location("c0031", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


async def _step(engine: AsyncEngine, name: str, *, batch_size: int | None = None):
    """Run one of the revision's steps on a connection the context commits on."""
    module = _load()
    if batch_size is not None:
        # Small enough that a handful of rows still walks several id ranges.
        module.BATCH_SIZE = batch_size

    def apply(sync_connection):
        # ``autocommit_block`` commits the context's own transaction, so the
        # context must have begun one: after a bare ``configure`` the block
        # asserts on ``_transaction``. env.py's ``transaction_per_migration``
        # is deliberately left off here, because under it
        # ``begin_transaction()`` returns a nullcontext and begins nothing --
        # and for a single revision the per-run transaction it begins instead
        # is that revision's transaction.
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
    name = f"c0031_replay_{uuid.uuid4().hex[:10]}"
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
async def pre_c0031(replay_engine: AsyncEngine) -> AsyncEngine:
    """The head schema taken back to the shape c0031 was written against.

    ``DROP COLUMN`` takes c0033's composite FK with it, so this is also the
    schema as it stood before that revision.
    """
    async with replay_engine.begin() as conn:
        await conn.execute(text(f"ALTER TABLE chunks DROP CONSTRAINT {FK_NAME}"))
        await conn.execute(text("ALTER TABLE chunks DROP COLUMN workspace_id"))
    return replay_engine


async def _corpus(engine: AsyncEngine, *, workspaces: int, chunks_each: int) -> None:
    """Workspaces, one document each, and unstamped chunks under them."""
    now = datetime.now(UTC)
    async with engine.begin() as conn:
        owner = uuid.uuid4()
        await conn.execute(
            text(
                'INSERT INTO "user" (id, email, hashed_password, is_active, '
                "is_superuser, is_verified) VALUES (:id, :email, 'x', true, "
                "false, true)"
            ),
            {"id": owner, "email": f"{owner.hex[:8]}@danvics.test"},
        )
        for n in range(workspaces):
            workspace_id = await conn.scalar(
                text(
                    "INSERT INTO workspaces (name, citations_enabled, user_id, "
                    "created_at) VALUES (:name, true, :owner, :now) RETURNING id"
                ),
                {"name": f"Space {n}", "owner": owner, "now": now},
            )
            document_id = await conn.scalar(
                text(
                    "INSERT INTO documents (title, document_type, content, "
                    "content_hash, workspace_id, created_at) VALUES (:title, "
                    "'LOCAL_FOLDER_FILE', 'body', :hash, :workspace, :now) "
                    "RETURNING id"
                ),
                {
                    "title": f"Doc {n}",
                    "hash": f"hash-{n}",
                    "workspace": workspace_id,
                    "now": now,
                },
            )
            for position in range(chunks_each):
                await conn.execute(
                    text(
                        "INSERT INTO chunks (content, position, document_id, "
                        "created_at) VALUES (:content, :position, :document, :now)"
                    ),
                    {
                        "content": f"chunk {n}.{position}",
                        "position": position,
                        "document": document_id,
                        "now": now,
                    },
                )


async def _disagreements(engine: AsyncEngine) -> int:
    """Chunks whose workspace is not the one their document belongs to."""
    async with engine.connect() as conn:
        return await conn.scalar(
            text(
                "SELECT count(*) FROM chunks JOIN documents "
                "ON documents.id = chunks.document_id "
                "WHERE chunks.workspace_id IS DISTINCT FROM documents.workspace_id"
            )
        )


async def _column_exists(engine: AsyncEngine) -> bool:
    async with engine.connect() as conn:
        return bool(
            await conn.scalar(
                text(
                    "SELECT count(*) FROM information_schema.columns "
                    "WHERE table_name = 'chunks' AND column_name = 'workspace_id'"
                )
            )
        )


async def _index_exists(engine: AsyncEngine) -> bool:
    async with engine.connect() as conn:
        return bool(
            await conn.scalar(
                text("SELECT count(*) FROM pg_indexes WHERE indexname = :name"),
                {"name": INDEX_NAME},
            )
        )


async def _fk_validated(engine: AsyncEngine) -> bool | None:
    async with engine.connect() as conn:
        return await conn.scalar(
            text("SELECT convalidated FROM pg_constraint WHERE conname = :name"),
            {"name": FK_NAME},
        )


async def _stamps(engine: AsyncEngine) -> list[tuple[int, int]]:
    async with engine.connect() as conn:
        rows = await conn.execute(
            text("SELECT id, workspace_id FROM chunks ORDER BY id")
        )
        return [tuple(row) for row in rows]


async def test_the_backfill_gives_every_chunk_its_documents_workspace(pre_c0031):
    await _corpus(pre_c0031, workspaces=3, chunks_each=5)

    await _step(pre_c0031, "upgrade", batch_size=2)

    assert await _disagreements(pre_c0031) == 0
    assert len(await _stamps(pre_c0031)) == 15


async def test_the_column_index_and_a_validated_fk_are_all_there_afterwards(
    pre_c0031,
):
    await _corpus(pre_c0031, workspaces=1, chunks_each=3)

    await _step(pre_c0031, "upgrade", batch_size=2)

    assert await _column_exists(pre_c0031)
    assert await _index_exists(pre_c0031)
    assert await _fk_validated(pre_c0031) is True


async def test_running_it_again_leaves_the_stamps_and_the_index_alone(pre_c0031):
    await _corpus(pre_c0031, workspaces=2, chunks_each=4)
    await _step(pre_c0031, "upgrade", batch_size=2)
    before = await _stamps(pre_c0031)

    await _step(pre_c0031, "upgrade", batch_size=2)

    assert await _stamps(pre_c0031) == before
    assert await _index_exists(pre_c0031)
    assert await _fk_validated(pre_c0031) is True


async def test_a_chunk_stamped_for_a_workspace_that_is_gone_fails_the_fk_validate(
    pre_c0031,
):
    """What a half-finished earlier run leaves behind, and why VALIDATE is separate."""
    await _corpus(pre_c0031, workspaces=1, chunks_each=3)
    async with pre_c0031.begin() as conn:
        await conn.execute(text("ALTER TABLE chunks ADD COLUMN workspace_id INTEGER"))
        await conn.execute(
            text(
                "UPDATE chunks SET workspace_id = 987654 "
                "WHERE id = (SELECT min(id) FROM chunks)"
            )
        )

    with pytest.raises(DBAPIError) as refused:
        await _step(pre_c0031, "upgrade", batch_size=2)

    assert FK_NAME in str(refused.value)


async def test_a_chunk_stamped_for_the_wrong_real_workspace_passes_every_check(
    pre_c0031,
):
    """The hole c0031 and c0032 together leave open, and c0033 closes.

    Both constraints read the column alone -- the FK only asks that the
    workspace exists, NOT NULL only that a value is there. Neither compares it
    with the chunk's document, so a write path that stamps the wrong real
    workspace hides the chunk from its own workspace's search and offers it to
    another's, exactly the corruption c0031's docstring describes, with no
    error anywhere.

    Still true of these two revisions on their own, which is why it stays:
    ``pre_c0031`` drops the column, and with it the composite FK c0033 adds, so
    this replay sees the schema as it stood between c0032 and c0033. What
    refuses the row now is
    ``tests/integration/test_chunks_document_workspace_c0033_migration.py``.
    """
    await _corpus(pre_c0031, workspaces=2, chunks_each=2)
    async with pre_c0031.begin() as conn:
        await conn.execute(text("ALTER TABLE chunks ADD COLUMN workspace_id INTEGER"))
        await conn.execute(
            text(
                "UPDATE chunks SET workspace_id = "
                "(SELECT max(id) FROM workspaces) "
                "WHERE id = (SELECT min(id) FROM chunks)"
            )
        )

    await _step(pre_c0031, "upgrade", batch_size=2)

    assert await _fk_validated(pre_c0031) is True
    assert await _disagreements(pre_c0031) == 1


async def test_downgrade_takes_the_column_index_and_fk_away_and_upgrade_returns_them(
    pre_c0031,
):
    await _corpus(pre_c0031, workspaces=2, chunks_each=3)
    await _step(pre_c0031, "upgrade", batch_size=2)

    await _step(pre_c0031, "downgrade")

    assert not await _column_exists(pre_c0031)
    assert not await _index_exists(pre_c0031)
    assert await _fk_validated(pre_c0031) is None

    await _step(pre_c0031, "upgrade", batch_size=2)

    assert await _column_exists(pre_c0031)
    assert await _index_exists(pre_c0031)
    assert await _disagreements(pre_c0031) == 0
