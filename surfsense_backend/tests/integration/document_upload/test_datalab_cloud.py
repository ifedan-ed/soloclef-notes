"""Datalab behind the Cloud choice, over the real HTTP API and Postgres.

With ``ETL_CLOUD_SERVICE=DATALAB`` an upload that picks Cloud goes through
the real Datalab adapter; only Datalab's HTTP endpoint is faked (an
``httpx.MockTransport``). What must not change from the LlamaCloud path:
the capabilities the picker reads, the per-page Cloud billing, the page
stamps on chunks, and a failed parse costing nothing. LlamaCloud stays the
fallback when Datalab fails.

Requires PostgreSQL + pgvector.
"""

from __future__ import annotations

from unittest.mock import AsyncMock

import httpx
import pytest
from sqlalchemy import select, text
from sqlalchemy.ext.asyncio import create_async_engine
from sqlalchemy.pool import NullPool

from app.etl_pipeline.parsers import datalab
from tests.integration.conftest import TEST_DATABASE_URL
from tests.utils.helpers import poll_document_status, upload_file

pytestmark = pytest.mark.integration

_CLOUD = {"parser": "cloud"}
_CHECK = "https://datalab.test/api/v1/convert/req-int"
_MARK = "-" * 48


class _Datalab:
    """Datalab's HTTP API as the adapter sees it: submit, then one poll."""

    def __init__(self, markdown: str, *, submit_status: int = 200):
        self.markdown = markdown
        self.submit_status = submit_status
        self.requests: list[httpx.Request] = []

    def __call__(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        if request.method == "POST":
            if self.submit_status != 200:
                return httpx.Response(self.submit_status, json={"detail": "refused"})
            return httpx.Response(
                200, json={"success": True, "request_check_url": _CHECK}
            )
        return httpx.Response(
            200,
            json={
                "status": "complete",
                "success": True,
                "markdown": self.markdown,
                "page_count": 2,
                "total_cost": 0.8,
            },
        )


@pytest.fixture
def datalab_cloud(monkeypatch):
    monkeypatch.setattr("app.config.config.ETL_DEFAULT_PARSER", "LOCAL")
    monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", "DATALAB")
    monkeypatch.setattr("app.config.config.DATALAB_API_KEY", "test-datalab-key")
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", "test-key")
    monkeypatch.setattr("app.config.config.AZURE_DI_ENDPOINT", None)
    monkeypatch.setattr("app.config.config.AZURE_DI_KEY", None)
    # Read fresh, never recalled from a parse cache an earlier test filled.
    monkeypatch.setattr(
        "app.etl_pipeline.cache.cached_extraction._recall",
        AsyncMock(return_value=None),
    )


@pytest.fixture
def fake_datalab(monkeypatch):
    """Route the real adapter to a scripted Datalab; returns the installer."""
    original = datalab.parse_with_datalab

    def install(markdown: str, *, submit_status: int = 200) -> _Datalab:
        server = _Datalab(markdown, submit_status=submit_status)

        async def through_fake(file_path, filename, estimated_pages):
            return await original(
                file_path,
                filename,
                estimated_pages,
                transport=httpx.MockTransport(server),
                poll_interval=0,
                base_delay=0,
            )

        monkeypatch.setattr(datalab, "parse_with_datalab", through_fake)
        return server

    return install


@pytest.fixture
def llamacloud_runs(monkeypatch):
    ran: list[str] = []

    async def llamacloud(file_path: str, estimated_pages: int) -> str:
        ran.append(file_path)
        return "# LlamaCloud fallback\n\nRead by LlamaCloud after Datalab failed."

    monkeypatch.setattr(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud", llamacloud
    )
    return ran


@pytest.fixture
def chunks_by_paragraph(monkeypatch):
    """Chunk on blank lines, so each page's text becomes its own chunk."""

    def by_paragraph(content, *args, **kwargs):
        return [part.strip() for part in content.split("\n\n") if part.strip()]

    for name in ("chunk_text", "chunk_text_hybrid"):
        monkeypatch.setattr(
            f"app.indexing_pipeline.cache.cached_indexing.{name}", by_paragraph
        )


async def _upload_cloud(client, headers, workspace_id, name, cleanup):
    resp = await upload_file(
        client,
        headers,
        "sample.pdf",
        workspace_id=workspace_id,
        filename_override=name,
        form=_CLOUD,
    )
    assert resp.status_code == 200, resp.text
    doc_ids = resp.json()["document_ids"]
    cleanup.extend(doc_ids)
    statuses = await poll_document_status(
        client, headers, doc_ids, workspace_id=workspace_id, timeout=300.0
    )
    return doc_ids, statuses


async def _chunk_pages(document_id: int) -> list[tuple[str, int | None]]:
    engine = create_async_engine(TEST_DATABASE_URL, poolclass=NullPool)
    try:
        async with engine.connect() as conn:
            rows = await conn.execute(
                text(
                    "SELECT content, page_start FROM chunks "
                    "WHERE document_id = :d ORDER BY position, id"
                ),
                {"d": document_id},
            )
            return [(row.content, row.page_start) for row in rows]
    finally:
        await engine.dispose()


async def _usage_rows() -> list[dict]:
    from app.billing.persistence.models import BillingUsage

    engine = create_async_engine(TEST_DATABASE_URL, poolclass=NullPool)
    try:
        async with engine.connect() as conn:
            rows = await conn.execute(
                select(
                    BillingUsage.action,
                    BillingUsage.units,
                    BillingUsage.charged_micros,
                ).order_by(BillingUsage.id.desc())
            )
            return [dict(row._mapping) for row in rows]
    finally:
        await engine.dispose()


async def _balance(client, headers) -> int:
    resp = await client.get("/users/me", headers=headers)
    assert resp.status_code == 200, resp.text
    return resp.json()["credit_micros_balance"]


async def test_capabilities_show_cloud_as_datalab_with_llamacloud_types(
    client: httpx.AsyncClient, headers: dict[str, str], datalab_cloud
):
    body = (await client.get("/api/v1/etl/capabilities", headers=headers)).json()
    cloud = next(entry for entry in body["parsers"] if entry["parser"] == "cloud")
    assert cloud["service"] == "DATALAB"
    assert cloud["available"] is True
    assert cloud["page_multiplier"] == 10
    # Datalab's own types, and LlamaCloud's for the rest (e.g. .rtf).
    assert {".pdf", ".docx", ".xlsx", ".rtf"} <= set(cloud["document_extensions"])
    # The picker is unchanged: still exactly Local and Cloud.
    assert sorted(entry["parser"] for entry in body["parsers"]) == ["cloud", "local"]


async def test_a_cloud_upload_is_read_by_datalab_and_stamped_with_pages(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    workspace_id: int,
    cleanup_doc_ids: list[int],
    datalab_cloud,
    fake_datalab,
    llamacloud_runs,
    chunks_by_paragraph,
):
    server = fake_datalab(
        f"{{0}}{_MARK}\n\nDatalab read the first page of the stamped test.\n\n"
        f"{{1}}{_MARK}\n\nDatalab read the second page of the stamped test.\n"
    )
    doc_ids, statuses = await _upload_cloud(
        client, headers, workspace_id, "datalab-pages.pdf", cleanup_doc_ids
    )
    assert statuses[doc_ids[0]]["status"]["state"] == "ready"
    assert [r.method for r in server.requests] == ["POST", "GET"]
    assert server.requests[0].headers["X-Api-Key"] == "test-datalab-key"
    assert b'name="mode"\r\n\r\nbalanced' in server.requests[0].content
    assert llamacloud_runs == []

    pages = dict(await _chunk_pages(doc_ids[0]))
    assert pages["Datalab read the first page of the stamped test."] == 1
    assert pages["Datalab read the second page of the stamped test."] == 2


async def test_datalab_pages_are_billed_as_cloud_pages(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    workspace_id: int,
    cleanup_doc_ids: list[int],
    datalab_cloud,
    fake_datalab,
    credits,
):
    fake_datalab("# Billed through Datalab\n\nCloud pages, same price as before.")
    await credits.set(balance_micros=credits.pages(1000))
    before = await _balance(client, headers)

    doc_ids, statuses = await _upload_cloud(
        client, headers, workspace_id, "datalab-billed.pdf", cleanup_doc_ids
    )
    assert statuses[doc_ids[0]]["status"]["state"] == "ready"

    charged = before - await _balance(client, headers)
    assert charged > 0
    assert charged % credits.micros_per_page == 0  # whole Cloud pages at 2 credits
    latest = (await _usage_rows())[0]
    assert latest["action"] == "cloud_page"
    assert latest["charged_micros"] == charged


async def test_datalab_failing_falls_back_to_llamacloud(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    workspace_id: int,
    cleanup_doc_ids: list[int],
    datalab_cloud,
    fake_datalab,
    llamacloud_runs,
):
    server = fake_datalab("unused", submit_status=401)
    doc_ids, statuses = await _upload_cloud(
        client, headers, workspace_id, "datalab-fallback.pdf", cleanup_doc_ids
    )
    assert statuses[doc_ids[0]]["status"]["state"] == "ready"
    assert [r.method for r in server.requests] == ["POST"]  # a 401 is not retried
    assert len(llamacloud_runs) == 1


async def test_datalab_failing_without_llamacloud_fails_the_document_and_costs_nothing(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    workspace_id: int,
    cleanup_doc_ids: list[int],
    datalab_cloud,
    fake_datalab,
    credits,
    monkeypatch,
):
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", None)
    fake_datalab("unused", submit_status=403)
    starting = credits.pages(1000)
    await credits.set(balance_micros=starting)

    doc_ids, statuses = await _upload_cloud(
        client, headers, workspace_id, "datalab-refused.pdf", cleanup_doc_ids
    )
    assert statuses[doc_ids[0]]["status"]["state"] == "failed"
    assert await _balance(client, headers) == starting
