"""c0032 on real Postgres: chunks.workspace_id becomes NOT NULL, or the migration fails.

``alembic/env.py`` fast-forwards an empty database with
``Base.metadata.create_all`` and stamps head, so this revision's SQL has never
run in a test -- only once, in production. The schema these tests start from is
therefore the head shape, and each one first puts ``chunks.workspace_id`` back
to the nullable column c0031 left behind before replaying the revision.

Everything here fits the transaction the session fixture rolls back: c0032 is
pure ``op.execute`` with no ``autocommit_block``, unlike c0031.
"""

from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest
import pytest_asyncio
from alembic.operations import Operations
from alembic.runtime.migration import MigrationContext
from sqlalchemy import text
from sqlalchemy.exc import DBAPIError
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Chunk, Document, DocumentType, User, Workspace

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[2]
    / "alembic"
    / "versions"
    / "c0032_chunks_workspace_id_not_null.py"
)

CHECK_NAME = "chunks_workspace_id_not_null"


def _step(session: AsyncSession, name: str):
    spec = importlib.util.spec_from_file_location("c0032", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    def apply(sync_session):
        module.op = Operations(MigrationContext.configure(sync_session.connection()))
        getattr(module, name)()

    return session.run_sync(apply)


@pytest_asyncio.fixture
async def pre_c0032_shape(db_session: AsyncSession) -> AsyncSession:
    """The nullable column c0031 leaves behind, which c0032 is written against."""
    await db_session.execute(
        text("ALTER TABLE chunks ALTER COLUMN workspace_id DROP NOT NULL")
    )
    return db_session


@pytest_asyncio.fixture
async def document(
    db_session: AsyncSession, db_user: User, db_workspace: Workspace
) -> Document:
    doc = Document(
        title="Chunked Doc",
        document_type=DocumentType.LOCAL_FOLDER_FILE,
        document_metadata={},
        content="Summary.",
        content_hash="c0032",
        workspace_id=db_workspace.id,
        created_by_id=db_user.id,
    )
    db_session.add(doc)
    await db_session.flush()
    return doc


async def _chunk(
    session: AsyncSession, document: Document, workspace_id: int | None
) -> Chunk:
    chunk = Chunk(
        content="A chunk of a document.",
        position=0,
        document_id=document.id,
        workspace_id=workspace_id,
    )
    session.add(chunk)
    await session.flush()
    return chunk


async def _is_nullable(session: AsyncSession) -> bool:
    answer = await session.scalar(
        text(
            "SELECT is_nullable FROM information_schema.columns "
            "WHERE table_name = 'chunks' AND column_name = 'workspace_id'"
        )
    )
    return answer == "YES"


async def _has_check(session: AsyncSession) -> bool:
    return bool(
        await session.scalar(
            text("SELECT count(*) FROM pg_constraint WHERE conname = :name"),
            {"name": CHECK_NAME},
        )
    )


async def test_a_stamped_corpus_gets_the_not_null_and_loses_the_scratch_check(
    pre_c0032_shape, document, db_workspace
):
    session = pre_c0032_shape
    await _chunk(session, document, db_workspace.id)
    assert await _is_nullable(session)

    await _step(session, "upgrade")

    assert not await _is_nullable(session)
    assert not await _has_check(session)


async def test_a_chunk_no_write_path_stamped_fails_the_validate(
    pre_c0032_shape, document
):
    session = pre_c0032_shape
    await _chunk(session, document, None)

    with pytest.raises(DBAPIError) as refused:
        async with session.begin_nested():
            await _step(session, "upgrade")

    assert CHECK_NAME in str(refused.value)
    assert await _is_nullable(session)


async def test_running_it_again_on_an_already_constrained_corpus_succeeds(
    pre_c0032_shape, document, db_workspace
):
    session = pre_c0032_shape
    await _chunk(session, document, db_workspace.id)

    await _step(session, "upgrade")
    await _step(session, "upgrade")

    assert not await _is_nullable(session)
    assert not await _has_check(session)


async def test_downgrade_takes_the_column_back_to_accepting_null(
    pre_c0032_shape, document, db_workspace
):
    session = pre_c0032_shape
    await _chunk(session, document, db_workspace.id)
    await _step(session, "upgrade")

    await _step(session, "downgrade")

    assert await _is_nullable(session)
    assert not await _has_check(session)
    # The column accepting NULL again is what "reversed" means here, so prove
    # it with a write rather than with the catalog alone.
    assert await _chunk(session, document, None) is not None
