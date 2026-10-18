"""Datalab as the Cloud parser: request, polling, pages, errors, fallback.

Datalab is reached through a mocked HTTP transport; nothing leaves the box.
The promises pinned here: the key goes in ``X-Api-Key``; the mode is
balanced; page markers become page texts; a refused request is not
retried (a resubmission is billed again) while a transient failure is;
and with ``ETL_CLOUD_SERVICE=DATALAB`` the Cloud choice reads through
Datalab, with LlamaCloud behind it for what Datalab cannot open or fails on.
"""

from __future__ import annotations

import httpx
import pytest

from app.etl_pipeline.capabilities import (
    UNAVAILABLE_MISSING_API_KEY,
    page_multiplier_for,
    parser_capability,
)
from app.etl_pipeline.etl_document import DocumentParser, EtlRequest
from app.etl_pipeline.etl_pipeline_service import EtlPipelineService
from app.etl_pipeline.parser_selection import (
    ParserUnavailableError,
    resolve_upload_parser,
    route_document,
)
from app.etl_pipeline.parsers import datalab
from app.etl_pipeline.parsers.datalab import (
    DatalabError,
    datalab_mode,
    parse_with_datalab,
    split_pages,
)
from app.utils.file_extensions import (
    DATALAB_DOCUMENT_EXTENSIONS,
    DOCUMENT_EXTENSIONS,
    LLAMAPARSE_DOCUMENT_EXTENSIONS,
)

pytestmark = pytest.mark.unit

SUBMIT_URL = "https://datalab.test/api/v1/convert"
CHECK_URL = "https://datalab.test/api/v1/convert/req-1"
MARKER = "-" * 48
PAGED = f"{{0}}{MARKER}\n\n# Title\n\nFirst page.\n\n{{1}}{MARKER}\n\nSecond page.\n"


class FakeDatalab:
    """A scripted Datalab: one reply per submit, a queue of poll replies."""

    def __init__(self, submits=None, polls=None):
        self.submits = list(submits or [httpx.Response(200, json=_submitted())])
        self.polls = list(polls or [httpx.Response(200, json=_complete())])
        self.requests: list[httpx.Request] = []

    def __call__(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        if request.method == "POST":
            reply = self.submits.pop(0) if len(self.submits) > 1 else self.submits[0]
        else:
            reply = self.polls.pop(0) if len(self.polls) > 1 else self.polls[0]
        if isinstance(reply, Exception):
            raise reply
        return reply

    @property
    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self)

    def count(self, method: str) -> int:
        return sum(1 for r in self.requests if r.method == method)


def _submitted():
    return {"success": True, "request_id": "req-1", "request_check_url": CHECK_URL}


def _complete(markdown=PAGED, **extra):
    return {
        "status": "complete",
        "success": True,
        "markdown": markdown,
        "page_count": 2,
        "total_cost": 0.8,
        **extra,
    }


@pytest.fixture
def pdf(tmp_path):
    path = tmp_path / "paper.pdf"
    path.write_bytes(b"%PDF-1.4 fake " * 20)
    return path


async def _parse(pdf, fake, **kwargs):
    kwargs.setdefault("api_key", "secret-key")
    kwargs.setdefault("base_url", SUBMIT_URL)
    kwargs.setdefault("poll_interval", 0)
    kwargs.setdefault("base_delay", 0)
    return await parse_with_datalab(
        str(pdf), "paper.pdf", 2, transport=fake.transport, **kwargs
    )


def _form_fields(request: httpx.Request) -> dict[str, str]:
    body = request.content.decode("latin-1")
    fields = {}
    for name in ("output_format", "mode", "paginate", "disable_image_extraction"):
        marker = f'name="{name}"\r\n\r\n'
        start = body.index(marker) + len(marker)
        fields[name] = body[start : body.index("\r\n", start)]
    return fields


# --- the request and the result ------------------------------------------


async def test_submits_with_the_key_and_balanced_mode_then_polls(pdf):
    fake = FakeDatalab(
        polls=[
            httpx.Response(200, json={"status": "processing"}),
            httpx.Response(200, json=_complete()),
        ]
    )
    content = await _parse(pdf, fake)

    submit = fake.requests[0]
    assert submit.method == "POST" and str(submit.url) == SUBMIT_URL
    assert submit.headers["X-Api-Key"] == "secret-key"
    assert _form_fields(submit) == {
        "output_format": "markdown",
        "mode": "balanced",
        "paginate": "true",
        "disable_image_extraction": "true",
    }
    assert b'filename="paper.pdf"' in submit.content
    assert b"application/pdf" in submit.content
    polls = [r for r in fake.requests if r.method == "GET"]
    assert len(polls) == 2 and all(str(r.url) == CHECK_URL for r in polls)
    assert all(r.headers["X-Api-Key"] == "secret-key" for r in polls)
    assert "First page." in content and "Second page." in content


async def test_page_markers_become_page_texts_and_leave_the_text(pdf):
    content = await _parse(pdf, FakeDatalab())
    assert MARKER not in content
    assert content.page_texts == ("# Title\n\nFirst page.", "Second page.")


async def test_a_mode_can_be_named_for_tests_and_tools(pdf):
    fake = FakeDatalab()
    await _parse(pdf, fake, mode="accurate")
    assert _form_fields(fake.requests[0])["mode"] == "accurate"


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("fast", "fast"),
        (" Accurate ", "accurate"),
        ("", "balanced"),
        ("turbo", "balanced"),
    ],
)
def test_an_unknown_or_empty_mode_is_balanced(value, expected):
    assert datalab_mode(value) == expected


def test_split_pages_keeps_text_before_the_first_marker_on_page_one():
    text, pages = split_pages(f"Preface\n{{0}}{MARKER}\nOne\n{{2}}{MARKER}\nThree\n")
    assert pages == ["Preface\nOne", "", "Three"]
    assert text == "Preface\nOne\n\nThree"


def test_split_pages_without_markers_returns_the_text_alone():
    assert split_pages("just text") == ("just text", None)


async def test_unpaginated_output_is_plain_text(pdf):
    content = await _parse(
        pdf, FakeDatalab(polls=[httpx.Response(200, json=_complete("Plain."))])
    )
    assert content == "Plain."
    assert getattr(content, "page_texts", None) is None


# --- errors ---------------------------------------------------------------


async def test_a_missing_key_fails_before_any_request(pdf, monkeypatch):
    monkeypatch.setattr("app.config.config.DATALAB_API_KEY", None)
    fake = FakeDatalab()
    with pytest.raises(DatalabError, match="DATALAB_API_KEY"):
        await parse_with_datalab(str(pdf), "paper.pdf", 1, transport=fake.transport)
    assert fake.requests == []


@pytest.mark.parametrize("status", [401, 403, 413, 422])
async def test_a_refused_request_is_not_retried(pdf, status):
    fake = FakeDatalab(submits=[httpx.Response(status, json={"detail": "no"})])
    with pytest.raises(DatalabError, match=str(status)):
        await _parse(pdf, fake)
    assert fake.count("POST") == 1


async def test_a_success_false_reply_is_not_retried(pdf):
    fake = FakeDatalab(
        submits=[httpx.Response(200, json={"success": False, "error": "bad file"})]
    )
    with pytest.raises(DatalabError, match="bad file"):
        await _parse(pdf, fake)
    assert fake.count("POST") == 1


async def test_a_failed_conversion_is_not_retried(pdf):
    fake = FakeDatalab(
        polls=[httpx.Response(200, json={"status": "failed", "error": "corrupt"})]
    )
    with pytest.raises(DatalabError, match="corrupt"):
        await _parse(pdf, fake)
    assert fake.count("POST") == 1


async def test_a_complete_reply_with_an_error_fails(pdf):
    fake = FakeDatalab(
        polls=[httpx.Response(200, json=_complete(success=False, error="page limit"))]
    )
    with pytest.raises(DatalabError, match="page limit"):
        await _parse(pdf, fake)


@pytest.mark.parametrize(
    "first",
    [
        httpx.Response(503, text="busy"),
        httpx.Response(429, text="slow down"),
        httpx.ConnectError("down"),
        httpx.ReadTimeout("slow"),
    ],
)
async def test_a_transient_submit_failure_is_retried(pdf, first):
    fake = FakeDatalab(submits=[first, httpx.Response(200, json=_submitted())])
    content = await _parse(pdf, fake)
    assert "First page." in content
    assert fake.count("POST") == 2


async def test_retries_stop_after_the_limit(pdf):
    fake = FakeDatalab(submits=[httpx.Response(502, text="bad gateway")])
    with pytest.raises(DatalabError, match="502"):
        await _parse(pdf, fake, max_retries=3)
    assert fake.count("POST") == 3


async def test_an_empty_result_is_retried_then_fails(pdf):
    fake = FakeDatalab(polls=[httpx.Response(200, json=_complete("  \n"))])
    with pytest.raises(DatalabError, match="empty"):
        await _parse(pdf, fake, max_retries=2)
    assert fake.count("POST") == 2


async def test_a_poll_hiccup_keeps_polling_the_same_job(pdf):
    fake = FakeDatalab(
        polls=[
            httpx.Response(503, text="busy"),
            httpx.ReadError("reset"),
            httpx.Response(200, json=_complete()),
        ]
    )
    await _parse(pdf, fake)
    assert fake.count("POST") == 1
    assert fake.count("GET") == 3


async def test_a_refused_poll_is_final(pdf):
    fake = FakeDatalab(polls=[httpx.Response(404, text="unknown request")])
    with pytest.raises(DatalabError, match="404"):
        await _parse(pdf, fake)
    assert fake.count("POST") == 1


async def test_a_job_that_outlives_the_timeout_is_not_resubmitted(pdf, monkeypatch):
    monkeypatch.setattr(datalab, "calculate_job_timeout", lambda pages, size: -1.0)
    fake = FakeDatalab(polls=[httpx.Response(200, json={"status": "processing"})])
    with pytest.raises(DatalabError, match="did not finish"):
        await _parse(pdf, fake)
    assert fake.count("POST") == 1


async def test_a_submit_without_a_check_url_fails(pdf):
    fake = FakeDatalab(submits=[httpx.Response(200, json={"success": True})])
    with pytest.raises(DatalabError, match="request_check_url"):
        await _parse(pdf, fake)


def test_the_error_message_never_carries_the_key():
    request = httpx.Request("POST", SUBMIT_URL, headers={"X-Api-Key": "secret-key"})
    with pytest.raises(DatalabError) as info:
        datalab._reply_json(
            httpx.Response(401, text="Invalid key", request=request), "x"
        )
    assert "secret-key" not in str(info.value)


# --- Datalab behind the Cloud choice ------------------------------------


@pytest.fixture
def datalab_cloud(monkeypatch):
    def configure(*, datalab_key="test-datalab-key", llama_key="test-key"):
        monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", "DATALAB")
        monkeypatch.setattr("app.config.config.DATALAB_API_KEY", datalab_key)
        monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", llama_key)

    configure()
    return configure


def test_the_cloud_service_defaults_to_llamacloud_and_accepts_datalab():
    from app.config.parser_settings import resolve_parser_settings

    assert resolve_parser_settings({}).cloud_service == "LLAMACLOUD"
    assert (
        resolve_parser_settings({"ETL_CLOUD_SERVICE": " datalab "}).cloud_service
        == "DATALAB"
    )


def test_cloud_is_datalab_when_configured(datalab_cloud):
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.service == "DATALAB"
    assert capability.available is True
    # Billing is untouched: the same Cloud rate as LlamaCloud.
    assert capability.page_multiplier == page_multiplier_for(DocumentParser.CLOUD) == 10
    assert resolve_upload_parser("cloud") is DocumentParser.CLOUD
    assert route_document("scan.pdf", DocumentParser.CLOUD).service == "DATALAB"


def test_cloud_opens_what_datalab_or_llamacloud_opens(datalab_cloud):
    extensions = parser_capability(DocumentParser.CLOUD).document_extensions
    assert extensions == DATALAB_DOCUMENT_EXTENSIONS | LLAMAPARSE_DOCUMENT_EXTENSIONS
    assert {
        ".pdf",
        ".docx",
        ".pptx",
        ".xlsx",
        ".epub",
        ".png",
    } <= DATALAB_DOCUMENT_EXTENSIONS
    assert DATALAB_DOCUMENT_EXTENSIONS <= DOCUMENT_EXTENSIONS


def test_without_a_llamacloud_key_cloud_opens_only_datalab_types(datalab_cloud):
    datalab_cloud(llama_key=None)
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.available is True
    assert capability.document_extensions == DATALAB_DOCUMENT_EXTENSIONS


def test_without_a_datalab_key_cloud_is_unavailable(datalab_cloud):
    datalab_cloud(datalab_key=None)
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.available is False
    assert capability.unavailable_reason == UNAVAILABLE_MISSING_API_KEY
    with pytest.raises(ParserUnavailableError):
        resolve_upload_parser("cloud")


def _cloud_request(path, filename="paper.pdf"):
    return EtlRequest(
        file_path=str(path),
        filename=filename,
        estimated_pages=2,
        parser=DocumentParser.CLOUD,
    )


async def test_a_cloud_pdf_is_read_by_datalab(datalab_cloud, pdf, mocker):
    parse = mocker.patch(
        "app.etl_pipeline.parsers.datalab.parse_with_datalab",
        return_value="# From Datalab",
    )
    llama = mocker.patch("app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud")
    result = await EtlPipelineService().extract(_cloud_request(pdf))
    parse.assert_awaited_once_with(str(pdf), "paper.pdf", 2)
    llama.assert_not_called()
    assert result.etl_service == "DATALAB"
    assert result.markdown_content == "# From Datalab"


async def test_datalab_failing_falls_back_to_llamacloud(datalab_cloud, pdf, mocker):
    mocker.patch(
        "app.etl_pipeline.parsers.datalab.parse_with_datalab",
        side_effect=DatalabError("Datalab conversion failed: corrupt"),
    )
    llama = mocker.patch(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        return_value="# From LlamaCloud",
    )
    result = await EtlPipelineService().extract(_cloud_request(pdf))
    llama.assert_awaited_once_with(str(pdf), 2)
    assert result.markdown_content == "# From LlamaCloud"
    assert result.etl_service == "DATALAB"


async def test_datalab_failing_without_llamacloud_reports_the_error(
    datalab_cloud, pdf, mocker
):
    datalab_cloud(llama_key=None)
    mocker.patch(
        "app.etl_pipeline.parsers.datalab.parse_with_datalab",
        side_effect=DatalabError("Datalab refused the request (401): bad key"),
    )
    with pytest.raises(DatalabError, match="401"):
        await EtlPipelineService().extract(_cloud_request(pdf))


async def test_a_type_datalab_cannot_open_goes_to_llamacloud(
    datalab_cloud, tmp_path, mocker
):
    rtf = tmp_path / "notes.rtf"
    rtf.write_bytes(b"{\\rtf1 hello}")
    parse = mocker.patch("app.etl_pipeline.parsers.datalab.parse_with_datalab")
    llama = mocker.patch(
        "app.etl_pipeline.parsers.llamacloud.parse_with_llamacloud",
        return_value="hello",
    )
    result = await EtlPipelineService().extract(_cloud_request(rtf, "notes.rtf"))
    parse.assert_not_called()
    llama.assert_awaited_once()
    assert result.markdown_content == "hello"


async def test_the_pipeline_keeps_datalab_pages(datalab_cloud, pdf, monkeypatch):
    fake = FakeDatalab()
    original = datalab.parse_with_datalab

    async def through_fake(file_path, filename, pages):
        return await original(
            file_path, filename, pages, transport=fake.transport, poll_interval=0
        )

    monkeypatch.setattr(datalab, "parse_with_datalab", through_fake)
    result = await EtlPipelineService().extract(_cloud_request(pdf))
    assert result.page_texts == ["# Title\n\nFirst page.", "Second page."]
    assert fake.requests[0].headers["X-Api-Key"] == "test-datalab-key"
    assert _form_fields(fake.requests[0])["mode"] == "balanced"
