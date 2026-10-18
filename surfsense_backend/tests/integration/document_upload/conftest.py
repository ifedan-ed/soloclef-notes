"""Integration conftest — runs the FastAPI app in-process via ASGITransport.

Prerequisites: PostgreSQL + pgvector only.

External system boundaries are mocked:
  - ETL parsing — LlamaParse (external API) and pymupdf4llm (heavy library)
  - LLM summarization, text embedding, text chunking (external APIs)
  - Redis heartbeat (external infrastructure)
  - Task dispatch is swapped via DI (InlineTaskDispatcher)
"""

from __future__ import annotations

import contextlib
import os
from collections.abc import AsyncGenerator
from unittest.mock import AsyncMock, MagicMock

import asyncpg
import httpx
import pytest
from httpx import ASGITransport
from sqlalchemy import text
from sqlalchemy.ext.asyncio import create_async_engine
from sqlalchemy.pool import NullPool

from app.app import app, limiter
from app.config import config as app_config
from app.db import Base
from app.services.task_dispatcher import get_task_dispatcher
from tests.integration.conftest import TEST_DATABASE_URL
from tests.utils.helpers import (
    TEST_EMAIL,
    auth_headers,
    delete_document,
    get_auth_token,
    get_workspace_id,
)

limiter.enabled = False

_EMBEDDING_DIM = app_config.embedding_model_instance.dimension
_ASYNCPG_URL = TEST_DATABASE_URL.replace("postgresql+asyncpg://", "postgresql://")

pytestmark = pytest.mark.integration


# ---------------------------------------------------------------------------
# Inline task dispatcher (replaces Celery via DI — not a mock)
# ---------------------------------------------------------------------------


class InlineTaskDispatcher:
    """Processes files synchronously in the calling coroutine.

    Swapped in via FastAPI dependency_overrides so the upload endpoint
    processes documents inline instead of dispatching to Celery.

    Exceptions are caught to match Celery's fire-and-forget semantics —
    the processing function already marks documents as failed internally.
    """

    async def dispatch_file_processing(
        self,
        *,
        document_id: int,
        temp_path: str,
        filename: str,
        workspace_id: int,
        user_id: str,
        use_vision_llm: bool = False,
        parser: str | None = None,
    ) -> None:
        from app.tasks.celery_tasks.document_tasks import (
            _process_file_with_document,
        )

        with contextlib.suppress(Exception):
            await _process_file_with_document(
                document_id,
                temp_path,
                filename,
                workspace_id,
                user_id,
                use_vision_llm=use_vision_llm,
                parser=parser,
            )


app.dependency_overrides[get_task_dispatcher] = lambda: InlineTaskDispatcher()


@pytest.fixture(autouse=True)
def _force_legacy_indexing(monkeypatch):
    """Pin the git-store master switch off for the upload-pipeline suite.

    These tests exercise the upload pipeline (ETL, dedup, credits, limits,
    search) on the Postgres-inline indexer, so they must not depend on the
    process ``KNOWLEDGE_STORE_ENABLED`` — a dev ``.env`` that turns it on would
    route uploads through the git store, whose async convergence no in-process
    broker runs. Git-native indexing is covered by the knowledge_store and
    artifacts suites.
    """
    monkeypatch.setattr(app_config, "KNOWLEDGE_STORE_ENABLED", False)


# ---------------------------------------------------------------------------
# Database setup (ASGITransport skips the app lifespan)
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session")
async def _ensure_tables():
    """Create DB tables and extensions once per session."""
    engine = create_async_engine(TEST_DATABASE_URL, poolclass=NullPool)
    async with engine.begin() as conn:
        await conn.execute(text("CREATE EXTENSION IF NOT EXISTS vector"))
        await conn.execute(text("CREATE EXTENSION IF NOT EXISTS pg_trgm"))
        await conn.run_sync(Base.metadata.create_all)
    await engine.dispose()


# ---------------------------------------------------------------------------
# Auth & workspace (session-scoped, via the in-process app)
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session")
async def auth_token(_ensure_tables) -> str:
    """Authenticate once per session, registering the user if needed."""
    async with httpx.AsyncClient(
        transport=ASGITransport(app=app), base_url="http://test", timeout=30.0
    ) as c:
        return await get_auth_token(c)


@pytest.fixture(scope="session")
async def workspace_id(auth_token: str) -> int:
    """Discover the first workspace belonging to the test user."""
    async with httpx.AsyncClient(
        transport=ASGITransport(app=app), base_url="http://test", timeout=30.0
    ) as c:
        return await get_workspace_id(c, auth_token)


@pytest.fixture(scope="session")
def headers(auth_token: str) -> dict[str, str]:
    return auth_headers(auth_token)


# ---------------------------------------------------------------------------
# Per-test HTTP client & cleanup
# ---------------------------------------------------------------------------


@pytest.fixture
async def client() -> AsyncGenerator[httpx.AsyncClient]:
    """Per-test async HTTP client using ASGITransport (no running server)."""
    async with httpx.AsyncClient(
        transport=ASGITransport(app=app), base_url="http://test", timeout=180.0
    ) as c:
        yield c


@pytest.fixture
def cleanup_doc_ids() -> list[int]:
    """Accumulator for document IDs that should be deleted after the test."""
    return []


@pytest.fixture(scope="session", autouse=True)
async def _purge_test_workspace(workspace_id: int):
    """Delete stale documents from previous runs before the session starts."""
    conn = await asyncpg.connect(_ASYNCPG_URL)
    try:
        result = await conn.execute(
            "DELETE FROM documents WHERE workspace_id = $1",
            workspace_id,
        )
        deleted = int(result.split()[-1])
        if deleted:
            print(
                f"\n[purge] Deleted {deleted} stale document(s) "
                f"from workspace {workspace_id}"
            )
    finally:
        await conn.close()
    yield


@pytest.fixture(autouse=True)
async def _cleanup_documents(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    cleanup_doc_ids: list[int],
):
    """Delete test documents after every test (API first, DB fallback)."""
    yield

    remaining_ids: list[int] = []
    for doc_id in cleanup_doc_ids:
        try:
            resp = await delete_document(client, headers, doc_id)
            if resp.status_code != 200:
                remaining_ids.append(doc_id)
        except Exception:
            remaining_ids.append(doc_id)

    if remaining_ids:
        conn = await asyncpg.connect(_ASYNCPG_URL)
        try:
            await conn.execute(
                "DELETE FROM documents WHERE id = ANY($1::int[])",
                remaining_ids,
            )
        finally:
            await conn.close()


# ---------------------------------------------------------------------------
# Credit-wallet helpers (direct DB for setup, API for verification)
# ---------------------------------------------------------------------------


async def _get_user_credit(email: str) -> tuple[int, int]:
    conn = await asyncpg.connect(_ASYNCPG_URL)
    try:
        row = await conn.fetchrow(
            "SELECT credit_micros_balance, credit_micros_reserved "
            'FROM "user" WHERE email = $1',
            email,
        )
        assert row is not None, f"User {email!r} not found in database"
        return row["credit_micros_balance"], row["credit_micros_reserved"]
    finally:
        await conn.close()


async def _set_user_credit(
    email: str, *, balance_micros: int, reserved_micros: int = 0
) -> None:
    conn = await asyncpg.connect(_ASYNCPG_URL)
    try:
        await conn.execute(
            'UPDATE "user" SET credit_micros_balance = $1, '
            "credit_micros_reserved = $2 WHERE email = $3",
            balance_micros,
            reserved_micros,
            email,
        )
    finally:
        await conn.close()


@pytest.fixture
async def credits():
    """Put the test user under live billing on a plan with Cloud parsing and no
    plan credits, so the top-up wallet (``credit_micros_balance``, set here
    directly) is all they can spend. Restores the mode, plan and balance after.

    ``pages(n)`` is what ``n`` Cloud pages cost at the seeded price.
    """
    from sqlalchemy import delete, select
    from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

    from app.billing import ledger
    from app.billing.persistence.models import BillingPlan, BillingSetting
    from app.billing.seed import ensure_catalog
    from app.billing.settings_store import save_settings
    from app.db import User

    engine = create_async_engine(TEST_DATABASE_URL)
    factory = async_sessionmaker(engine, expire_on_commit=False)
    async with factory() as session:
        await ensure_catalog(session)
        user_id = await session.scalar(select(User.id).where(User.email == TEST_EMAIL))
        await save_settings(session, values={"mode": "live"})
        account, user = await ledger.lock_account(session, user_id)
        original_plan = account.plan_id
        plus = await session.scalar(select(BillingPlan).where(BillingPlan.slug == "plus"))
        await ledger.change_plan(session, account, user, plus, source="admin", reason="test")
        account.plan_credits_micros = 0
        await session.commit()
    cloud_page = 20_000

    class _Credits:
        micros_per_page = cloud_page

        async def set(self, *, balance_micros: int, reserved_micros: int = 0) -> None:
            await _set_user_credit(
                TEST_EMAIL,
                balance_micros=balance_micros,
                reserved_micros=reserved_micros,
            )

        def pages(self, n: int) -> int:
            return n * cloud_page

    original = await _get_user_credit(TEST_EMAIL)
    try:
        yield _Credits()
    finally:
        await _set_user_credit(
            TEST_EMAIL, balance_micros=original[0], reserved_micros=original[1]
        )
        async with factory() as session:
            await session.execute(delete(BillingSetting).where(BillingSetting.key == "mode"))
            account, user = await ledger.lock_account(session, user_id)
            account.plan_id = original_plan
            account.plan_source = "default"
            await session.commit()
        await engine.dispose()


# ---------------------------------------------------------------------------
# Mock external system boundaries
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def _mock_external_apis(monkeypatch):
    """Mock LLM, embedding, and chunking — these are external API boundaries."""
    monkeypatch.setattr(
        "app.indexing_pipeline.cache.cached_indexing.embed_texts",
        MagicMock(side_effect=lambda texts: [[0.1] * _EMBEDDING_DIM for _ in texts]),
    )
    monkeypatch.setattr(
        "app.indexing_pipeline.cache.cached_indexing.chunk_text",
        MagicMock(return_value=["Test chunk content."]),
    )


@pytest.fixture(autouse=True)
def _mock_celery_delete_task(monkeypatch):
    """Mock Celery delete dispatch — no broker is available in CI."""
    monkeypatch.setattr(
        "app.tasks.celery_tasks.document_tasks.delete_document_task.delay",
        lambda *args, **kwargs: None,
    )


@pytest.fixture(autouse=True)
def _mock_redis_heartbeat(monkeypatch):
    """Mock Redis heartbeat — Redis is an external infrastructure boundary."""
    monkeypatch.setattr(
        "app.tasks.celery_tasks.document_tasks._start_heartbeat",
        lambda notification_id: None,
    )
    monkeypatch.setattr(
        "app.tasks.celery_tasks.document_tasks._stop_heartbeat",
        lambda notification_id: None,
    )
    monkeypatch.setattr(
        "app.tasks.celery_tasks.document_tasks._run_heartbeat_loop",
        AsyncMock(),
    )


_MOCK_ETL_MARKDOWN = "# Mocked Document\n\nThis is mocked ETL content."


@pytest.fixture(autouse=True)
def _mock_etl_parsing(monkeypatch):
    """Mock ETL parsing — LlamaParse and pymupdf4llm are external boundaries.

    Preserves the real contract: empty/corrupt files raise an error just like
    the actual services would, so tests covering failure paths keep working.
    """

    def _reject_empty(file_path: str) -> None:
        if os.path.getsize(file_path) == 0:
            raise RuntimeError(f"Cannot parse empty file: {file_path}")

    # -- LlamaParse mock (external API) --------------------------------

    async def _fake_llamacloud_parse(file_path: str, estimated_pages: int) -> str:
        _reject_empty(file_path)
        return _MOCK_ETL_MARKDOWN

    monkeypatch.setattr(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        _fake_llamacloud_parse,
    )

    # -- pymupdf4llm mock (heavy library boundary) ---------------------

    async def _fake_pymupdf4llm_parse(file_path: str, filename: str) -> str:
        _reject_empty(file_path)
        return _MOCK_ETL_MARKDOWN

    monkeypatch.setattr(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm",
        _fake_pymupdf4llm_parse,
    )

    # ...and the library underneath it, in case anything reaches past
    # the parser module to call to_markdown directly.
    def _fake_to_markdown(file_path, **kwargs):
        _reject_empty(file_path)
        return _MOCK_ETL_MARKDOWN

    monkeypatch.setattr("pymupdf4llm.to_markdown", _fake_to_markdown)
