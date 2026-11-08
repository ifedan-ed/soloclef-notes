"""c0033 on real Postgres: a chunk can only be stamped for its document's workspace.

Companion to ``test_chunks_workspace_id_c0031_migration.py``, which records the
hole this revision closes: c0031's FK only asks that the workspace *exists* and
c0032 only that a value is *there*, so a chunk carrying a real but wrong
``workspace_id`` passes both and is then invisible to its own workspace's
search and offered to another's.

Replayed on a database of its own for the same reason c0031's tests are: the
revision builds its unique index with ``CREATE INDEX CONCURRENTLY`` inside an
``autocommit_block``, which commits and cannot run inside someone else's
transaction.
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
    / "c0033_chunks_document_workspace_fk.py"
)

UNIQUE_INDEX = "documents_id_workspace_id_key"
FK_NAME = "chunks_document_workspace_fkey"


def _load():
    spec = importlib.util.spec_from_file_location("c0033", _REVISION)
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
    name = f"c0033_replay_{uuid.uuid4().hex[:10]}"
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
async def pre_c0033(replay_engine: AsyncEngine) -> AsyncEngine:
    """The head schema taken back to the shape c0033 was written against.

    ``Base.metadata.create_all`` now declares both halves of this revision, so
    a replay has to undo them first -- the same reason c0031's tests carry a
    ``pre_c0031``.
    """
    async with replay_engine.begin() as conn:
        await conn.execute(text(f"ALTER TABLE chunks DROP CONSTRAINT {FK_NAME}"))
        await conn.execute(
            text(f"ALTER TABLE documents DROP CONSTRAINT {UNIQUE_INDEX}")
        )
    return replay_engine


async def _corpus(engine: AsyncEngine, *, workspaces: int) -> list[tuple[int, int]]:
    """Workspaces with one document and one correctly stamped chunk each."""
    now = datetime.now(UTC)
    pairs: list[tuple[int, int]] = []
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
            await conn.execute(
                text(
                    "INSERT INTO chunks (content, position, document_id, "
                    "workspace_id, created_at) VALUES ('body', 0, :document, "
                    ":workspace, :now)"
                ),
                {"document": document_id, "workspace": workspace_id, "now": now},
            )
            pairs.append((document_id, workspace_id))
    return pairs


async def _stamp(engine: AsyncEngine, document_id: int, workspace_id: int) -> None:
    async with engine.begin() as conn:
        await conn.execute(
            text(
                "INSERT INTO chunks (content, position, document_id, workspace_id, "
                "created_at) VALUES ('stray', 1, :document, :workspace, now())"
            ),
            {"document": document_id, "workspace": workspace_id},
        )


async def _fk_validated(engine: AsyncEngine) -> bool | None:
    async with engine.connect() as conn:
        return await conn.scalar(
            text("SELECT convalidated FROM pg_constraint WHERE conname = :name"),
            {"name": FK_NAME},
        )


async def _index_exists(engine: AsyncEngine) -> bool:
    async with engine.connect() as conn:
        return bool(
            await conn.scalar(
                text("SELECT count(*) FROM pg_indexes WHERE indexname = :name"),
                {"name": UNIQUE_INDEX},
            )
        )


async def test_a_chunk_stamped_for_the_wrong_real_workspace_is_refused(pre_c0033):
    pairs = await _corpus(pre_c0033, workspaces=2)
    (document_id, _), (_, other_workspace) = pairs

    await _step(pre_c0033, "upgrade")

    with pytest.raises(IntegrityError) as refused:
        await _stamp(pre_c0033, document_id, other_workspace)
    assert FK_NAME in str(refused.value)


async def test_the_right_workspace_still_passes(pre_c0033):
    [(document_id, workspace_id)] = await _corpus(pre_c0033, workspaces=1)

    await _step(pre_c0033, "upgrade")

    await _stamp(pre_c0033, document_id, workspace_id)


async def test_the_unique_index_and_a_validated_fk_are_there_afterwards(pre_c0033):
    await _corpus(pre_c0033, workspaces=2)

    await _step(pre_c0033, "upgrade")

    assert await _index_exists(pre_c0033)
    assert await _fk_validated(pre_c0033) is True


async def test_an_existing_disagreement_fails_the_validate(pre_c0033):
    """Loudly, as c0031 chose: a failed migration beats a silent hole."""
    pairs = await _corpus(pre_c0033, workspaces=2)
    (document_id, _), (_, other_workspace) = pairs
    await _stamp(pre_c0033, document_id, other_workspace)

    with pytest.raises(Exception) as refused:
        await _step(pre_c0033, "upgrade")

    assert FK_NAME in str(refused.value)


async def test_moving_a_document_restamps_its_chunks(pre_c0033):
    """``ON UPDATE CASCADE`` keeps the invariant true through a workspace move."""
    pairs = await _corpus(pre_c0033, workspaces=2)
    (document_id, _), (_, other_workspace) = pairs
    await _step(pre_c0033, "upgrade")

    async with pre_c0033.begin() as conn:
        await conn.execute(
            text("UPDATE documents SET workspace_id = :ws WHERE id = :id"),
            {"ws": other_workspace, "id": document_id},
        )

    async with pre_c0033.connect() as conn:
        stamps = await conn.scalars(
            text("SELECT workspace_id FROM chunks WHERE document_id = :id"),
            {"id": document_id},
        )
        assert set(stamps) == {other_workspace}


async def test_running_it_again_is_a_no_op(pre_c0033):
    await _corpus(pre_c0033, workspaces=2)
    await _step(pre_c0033, "upgrade")

    await _step(pre_c0033, "upgrade")

    assert await _fk_validated(pre_c0033) is True


async def test_downgrade_takes_the_fk_and_index_away_and_upgrade_returns_them(
    pre_c0033,
):
    await _corpus(pre_c0033, workspaces=2)
    await _step(pre_c0033, "upgrade")

    await _step(pre_c0033, "downgrade")

    assert await _fk_validated(pre_c0033) is None
    assert not await _index_exists(pre_c0033)

    await _step(pre_c0033, "upgrade")

    assert await _fk_validated(pre_c0033) is True
    assert await _index_exists(pre_c0033)
