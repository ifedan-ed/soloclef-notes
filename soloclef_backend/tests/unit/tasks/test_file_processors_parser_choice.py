"""The upload's Local/Cloud choice reaches the ETL request on every file path.

The non-document path (images, plaintext, audio) used to build its
``EtlRequest`` without the choice, so an image the vision LLM did not
read went to whatever parser the server liked rather than the one asked
for. Billing follows the parser that actually runs.
"""

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from app.etl_pipeline.etl_document import DocumentParser, EtlResult
from app.tasks.document_processors import file_processors

pytestmark = pytest.mark.unit


@pytest.fixture
def captured_requests(monkeypatch):
    requests = []

    async def extract(request, *, vision_llm=None):
        requests.append((request, vision_llm))
        return EtlResult(
            markdown_content="# text", etl_service="PYMUPDF4LLM", content_type="x"
        )

    monkeypatch.setattr("app.etl_pipeline.cache.extract_with_cache", extract)
    monkeypatch.setattr("app.config.config.ETL_DEFAULT_PARSER", "LOCAL")
    monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", "key")
    return requests


def _ctx(tmp_path, filename, parser, *, use_vision_llm=False):
    path = tmp_path / filename
    path.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 64)
    return file_processors._ProcessingContext(
        session=SimpleNamespace(),
        file_path=str(path),
        filename=filename,
        workspace_id=1,
        user_id="u",
        task_logger=SimpleNamespace(
            log_task_progress=AsyncMock(), log_task_success=AsyncMock()
        ),
        log_entry=SimpleNamespace(),
        use_vision_llm=use_vision_llm,
        parser=parser,
    )


@pytest.mark.parametrize("parser", ["cloud", "local"])
async def test_an_image_without_vision_carries_the_chosen_parser(
    tmp_path, monkeypatch, captured_requests, parser
):
    monkeypatch.setattr(
        file_processors, "add_received_markdown_file_document", AsyncMock()
    )

    await file_processors._process_non_document_upload(
        _ctx(tmp_path, "scan.png", parser)
    )

    request, vision_llm = captured_requests[0]
    assert request.parser is DocumentParser(parser)
    assert vision_llm is None


async def test_legacy_premium_on_the_non_document_path_means_cloud(
    tmp_path, monkeypatch, captured_requests
):
    monkeypatch.setattr(
        file_processors, "add_received_markdown_file_document", AsyncMock()
    )

    await file_processors._process_non_document_upload(
        _ctx(tmp_path, "scan.png", "premium")
    )

    assert captured_requests[0][0].parser is DocumentParser.CLOUD


async def test_connector_uploads_choose_no_parser(
    tmp_path, monkeypatch, captured_requests
):
    monkeypatch.setattr(
        file_processors, "add_received_markdown_file_document", AsyncMock()
    )

    await file_processors._process_non_document_upload(_ctx(tmp_path, "scan.png", None))

    assert captured_requests[0][0].parser is None


@pytest.mark.parametrize("parser", ["local", "cloud"])
async def test_two_phase_upload_bills_by_the_chosen_parser(
    tmp_path, monkeypatch, captured_requests, parser
):
    pdf = tmp_path / "report.pdf"
    pdf.write_bytes(b"%PDF-1.4 fake")
    checked = []

    class Credits:
        def __init__(self, _session):
            pass

        def estimate_pages_before_processing(self, _path):
            return 3

        async def check_credits(self, _user, pages, *, parser=None, workspace_id=None):
            checked.append((pages, parser))

    monkeypatch.setattr("app.services.etl_credit_service.EtlCreditService", Credits)

    _, _, billable = await file_processors._extract_file_content(
        str(pdf),
        "report.pdf",
        1,
        SimpleNamespace(),
        "u",
        SimpleNamespace(log_task_progress=AsyncMock()),
        SimpleNamespace(),
        None,
        parser=parser,
    )

    assert captured_requests[0][0].parser is DocumentParser(parser)
    # Pages are counted; app.billing prices them by the parser that reads them.
    assert billable == 3
    assert checked == [(3, DocumentParser(parser))]
