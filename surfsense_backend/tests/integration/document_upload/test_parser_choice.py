"""The per-upload Local/Cloud parser choice over the real HTTP API.

Requires PostgreSQL + pgvector. The parsers themselves are the external
boundary the suite's conftest fakes; these record which one ran.
"""

from __future__ import annotations

import httpx
import pytest

from tests.utils.helpers import FIXTURES_DIR, poll_document_status

pytestmark = pytest.mark.integration


@pytest.fixture
def cloud_available(monkeypatch):
    monkeypatch.setattr("app.config.config.ETL_DEFAULT_PARSER", "LOCAL")
    monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", "test-key")
    monkeypatch.setattr("app.config.config.AZURE_DI_ENDPOINT", None)
    monkeypatch.setattr("app.config.config.AZURE_DI_KEY", None)


@pytest.fixture
def parsers_run(monkeypatch):
    ran: list[str] = []

    async def local(file_path: str, filename: str) -> str:
        ran.append("local")
        return f"# Local text\n\nParsed {filename} on this box."

    async def cloud(file_path: str, estimated_pages: int) -> str:
        ran.append("cloud")
        return f"# Cloud text\n\nParsed {file_path} by the agentic tier."

    monkeypatch.setattr(
        "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm", local
    )
    monkeypatch.setattr(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud", cloud
    )
    return ran


async def _upload(client, headers, workspace_id, name, **form):
    with open(FIXTURES_DIR / "sample.pdf", "rb") as handle:
        return await client.post(
            "/api/v1/documents/fileupload",
            headers=headers,
            files={"files": (name, handle)},
            data={"workspace_id": str(workspace_id), **form},
        )


async def test_capabilities_report_both_parsers_and_their_file_types(
    client: httpx.AsyncClient, headers: dict[str, str], cloud_available
):
    resp = await client.get("/api/v1/etl/capabilities", headers=headers)
    assert resp.status_code == 200
    body = resp.json()

    assert body["default_parser"] == "local"
    parsers = {entry["parser"]: entry for entry in body["parsers"]}
    assert parsers["local"]["service"] == "PYMUPDF4LLM"
    assert parsers["local"]["page_multiplier"] == 0
    assert ".pdf" in parsers["local"]["document_extensions"]
    assert ".xlsx" not in parsers["local"]["document_extensions"]
    assert parsers["cloud"] == {
        **parsers["cloud"],
        "service": "LLAMACLOUD",
        "available": True,
        "unavailable_reason": None,
        "page_multiplier": 10,
    }
    assert ".xlsx" in parsers["cloud"]["document_extensions"]
    assert ".md" in body["always_handled"]["plaintext"]
    assert ".csv" in body["always_handled"]["direct_convert"]
    assert ".mp3" in body["always_handled"]["audio"]
    assert ".png" in body["always_handled"]["image"]


async def test_capabilities_explain_an_unavailable_cloud(
    client: httpx.AsyncClient, headers: dict[str, str], cloud_available, monkeypatch
):
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", None)
    body = (await client.get("/api/v1/etl/capabilities", headers=headers)).json()
    cloud = next(entry for entry in body["parsers"] if entry["parser"] == "cloud")
    assert cloud["available"] is False
    assert cloud["unavailable_reason"] == "missing_api_key"


@pytest.mark.parametrize(
    ("form", "expected"),
    [
        ({}, "local"),
        ({"parser": "local"}, "local"),
        ({"parser": "cloud"}, "cloud"),
        # Older web builds, the desktop sync and the MCP server.
        ({"processing_mode": "basic"}, "local"),
        ({"processing_mode": "premium"}, "cloud"),
    ],
)
async def test_the_chosen_parser_is_the_one_that_runs(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    workspace_id: int,
    cleanup_doc_ids: list[int],
    cloud_available,
    parsers_run,
    form,
    expected,
):
    label = "-".join(f"{key}-{value}" for key, value in form.items()) or "default"
    resp = await _upload(client, headers, workspace_id, f"choice-{label}.pdf", **form)
    assert resp.status_code == 200, resp.text
    doc_ids = resp.json()["document_ids"]
    cleanup_doc_ids.extend(doc_ids)

    statuses = await poll_document_status(
        client, headers, doc_ids, workspace_id=workspace_id
    )
    assert statuses[doc_ids[0]]["status"]["state"] == "ready"
    assert parsers_run == [expected]


async def test_a_file_the_chosen_parser_cannot_read_is_refused_before_upload(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    workspace_id: int,
    cloud_available,
    parsers_run,
):
    resp = await _upload(client, headers, workspace_id, "sheet.xlsx", parser="local")
    assert resp.status_code == 400
    assert resp.json()["detail"] == {
        "code": "unsupported_file_type",
        "parser": "local",
        "files": ["sheet.xlsx"],
    }
    assert parsers_run == []


async def test_an_explicit_cloud_choice_without_cloud_is_refused(
    client: httpx.AsyncClient,
    headers: dict[str, str],
    workspace_id: int,
    cloud_available,
    monkeypatch,
):
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", None)
    resp = await _upload(client, headers, workspace_id, "doc.pdf", parser="cloud")
    assert resp.status_code == 400
    assert resp.json()["detail"]["code"] == "parser_unavailable"
    assert resp.json()["detail"]["reason"] == "missing_api_key"
