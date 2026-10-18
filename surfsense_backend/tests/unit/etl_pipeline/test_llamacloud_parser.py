"""LlamaCloud as the Cloud parser: request, polling, pages, errors, fallback.

The real llama-cloud client talks to a mocked HTTP transport of LlamaCloud's
v2 Parse API; nothing leaves the box. The promises pinned here: the key goes
in ``Authorization: Bearer``; only v2 routes are called; the upload asks for
the agentic tier; the job is polled until it is COMPLETED and its pages
become page texts; a cache hit is logged and billed nothing; the client
retries a request by itself, and when that fails the upload is not
resubmitted while a poll is tried again; a refusal (bad key, a plan out of
credits) and a job that outlives its wait are raised at once; a failed job
or an empty result is resubmitted for a fresh parse; and with
``ETL_CLOUD_SERVICE=LLAMACLOUD`` the Cloud choice reads through it, while
with ``DATALAB`` it reads what Datalab cannot.
"""

from __future__ import annotations

import asyncio
import functools
import json
import types

import anyio
import httpx
import pytest
from llama_cloud import APIConnectionError, AuthenticationError, NotFoundError

from app.etl_pipeline.capabilities import parser_capability
from app.etl_pipeline.constants import (
    LLAMACLOUD_MAX_BASE_JOB_TIMEOUT,
    LLAMACLOUD_REQUEST_RETRIES,
)
from app.etl_pipeline.etl_document import DocumentParser, EtlRequest
from app.etl_pipeline.etl_pipeline_service import EtlPipelineService
from app.etl_pipeline.parsers import datalab, llamacloud
from app.etl_pipeline.parsers.llamacloud import (
    LLAMA_PARSE_TIER,
    LLAMA_PARSE_VERSION,
    LlamaCloudJobFailedError,
    LlamaCloudWaitExceededError,
    parse_with_llamacloud,
)
from app.utils.file_extensions import (
    DATALAB_DOCUMENT_EXTENSIONS,
    LLAMAPARSE_DOCUMENT_EXTENSIONS,
)

pytestmark = pytest.mark.unit

UPLOAD = "/api/v2/parse/upload"
JOB = "/api/v2/parse/pjb-1"
OUT_OF_CREDITS = {
    "detail": "You've exceeded the maximum number of credits for your plan."
}


def _created(status: str = "PENDING") -> httpx.Response:
    return httpx.Response(
        200, json={"id": "pjb-1", "project_id": "prj-1", "status": status}
    )


def _job(status: str, *pages, credits: float | None = None, error=None) -> dict:
    """A GET /api/v2/parse/{id} body; a page is its text, or None if it failed."""
    body: dict = {
        "job": {
            "id": "pjb-1",
            "project_id": "prj-1",
            "status": status,
            "error_message": error,
            "usage": {"credits": credits},
        }
    }
    if status == "COMPLETED":
        body["markdown"] = {
            "pages": [
                {"page_number": number, "success": True, "markdown": text}
                if text is not None
                else {"page_number": number, "success": False, "error": "unreadable"}
                for number, text in enumerate(pages, start=1)
            ]
        }
    return body


def _polled(status: str, *pages, **kwargs) -> httpx.Response:
    return httpx.Response(200, json=_job(status, *pages, **kwargs))


TWO_PAGES = ("# Title\n\nFirst page.", "Second page.")


class FakeLlamaCloud:
    """A scripted LlamaCloud: upload replies and a queue of poll replies.

    Each queue gives its replies in turn and then repeats its last; a reply
    may be an exception, raised as the transport's.
    """

    def __init__(self, uploads=None, polls=None):
        self.uploads = list(uploads or [_created()])
        self.polls = list(polls or [_polled("COMPLETED", *TWO_PAGES, credits=20.0)])
        self.requests: list[httpx.Request] = []

    def __call__(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        path = request.url.path
        if request.method == "POST" and path == UPLOAD:
            queue = self.uploads
        elif request.method == "GET" and path == JOB:
            queue = self.polls
        else:
            return httpx.Response(404, json={"detail": f"unexpected {path}"})
        reply = queue.pop(0) if len(queue) > 1 else queue[0]
        if isinstance(reply, Exception):
            raise reply
        return reply

    @property
    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self)

    def count(self, path: str) -> int:
        return sum(1 for r in self.requests if r.url.path == path)

    def configurations(self) -> list[dict]:
        return [
            json.loads(_form_field(r, "configuration"))
            for r in self.requests
            if r.url.path == UPLOAD
        ]


@pytest.fixture(autouse=True)
def client_waits(monkeypatch) -> list[float]:
    """The llama-cloud client's own waits between tries, recorded, not slept."""
    waits: list[float] = []
    real_sleep = asyncio.sleep

    async def record(seconds, *args, **kwargs):
        waits.append(seconds)
        await real_sleep(0)

    monkeypatch.setattr(anyio, "sleep", record)
    return waits


@pytest.fixture
def pdf(tmp_path):
    path = tmp_path / "paper.pdf"
    path.write_bytes(b"%PDF-1.4 fake " * 20)
    return path


def _options(fake: FakeLlamaCloud, **kwargs) -> dict:
    kwargs.setdefault("api_key", "secret-key")
    kwargs.setdefault("transport", fake.transport)
    kwargs.setdefault("poll_interval", 0)
    kwargs.setdefault("base_delay", 0)
    return kwargs


async def _parse(path, fake, pages=2, **kwargs):
    return await parse_with_llamacloud(str(path), pages, **_options(fake, **kwargs))


def _form_field(request: httpx.Request, name: str) -> str:
    body = request.content.decode("latin-1")
    marker = f'name="{name}"\r\n\r\n'
    start = body.index(marker) + len(marker)
    return body[start : body.index("\r\n", start)]


# --- the request and the result ------------------------------------------


async def test_uploads_with_the_key_in_the_agentic_tier_then_polls(pdf):
    fake = FakeLlamaCloud(
        polls=[
            _polled("PENDING"),
            _polled("RUNNING"),
            _polled("COMPLETED", *TWO_PAGES),
        ]
    )
    content = await _parse(pdf, fake)

    upload = fake.requests[0]
    assert upload.method == "POST" and upload.url.path == UPLOAD
    assert upload.headers["Authorization"] == "Bearer secret-key"
    assert b'name="file"; filename="paper.pdf"' in upload.content
    (configuration,) = fake.configurations()
    assert configuration["tier"] == LLAMA_PARSE_TIER == "agentic"
    assert configuration["version"] == LLAMA_PARSE_VERSION == "latest"
    assert configuration["processing_options"] == {
        "ocr_parameters": {"languages": ["en"]}
    }
    assert "disable_cache" not in configuration
    assert fake.count(JOB) == 3
    polls = [r for r in fake.requests if r.url.path == JOB]
    assert all(r.url.params.get_list("expand") == ["markdown", "usage"] for r in polls)
    assert all(r.headers["Authorization"] == "Bearer secret-key" for r in fake.requests)
    assert all(r.url.path.startswith("/api/v2/parse") for r in fake.requests)
    assert "First page." in content and "Second page." in content


async def test_the_job_may_run_the_job_timeout_plus_a_minute_a_page(pdf):
    fake = FakeLlamaCloud()
    await _parse(pdf, fake, pages=2)
    # app.etl_pipeline.constants: 600 s + 60 s a page, and 60 s a page more.
    assert fake.configurations()[0]["processing_control"] == {
        "timeouts": {"base_in_seconds": 720, "extra_time_per_page_in_seconds": 60}
    }


async def test_the_job_timeout_sent_is_capped_where_v2_caps_it(pdf):
    fake = FakeLlamaCloud()
    await _parse(pdf, fake, pages=500)
    timeouts = fake.configurations()[0]["processing_control"]["timeouts"]
    assert timeouts["base_in_seconds"] == LLAMACLOUD_MAX_BASE_JOB_TIMEOUT == 7200


async def test_pages_become_page_texts(pdf):
    content = await _parse(pdf, FakeLlamaCloud())
    assert content == "# Title\n\nFirst page.\n\nSecond page."
    assert content.page_texts == TWO_PAGES


async def test_a_page_llamacloud_could_not_read_stays_empty(pdf, caplog):
    caplog.set_level("INFO")
    fake = FakeLlamaCloud(polls=[_polled("COMPLETED", "One.", None, "Three.")])
    content = await _parse(pdf, fake)
    assert content.page_texts == ("One.", "", "Three.")
    assert "failed_pages=[2]" in caplog.text


async def test_the_page_count_and_the_credits_are_logged(pdf, caplog):
    caplog.set_level("INFO")
    await _parse(pdf, FakeLlamaCloud())
    assert "LlamaCloud parse done" in caplog.text
    assert "pages=2" in caplog.text
    assert "cache_hit=False credits=20.0" in caplog.text


async def test_a_cache_hit_is_logged_and_billed_nothing(pdf, caplog):
    # LlamaCloud answers the same file and options within 48 hours with the
    # earlier job, already COMPLETED; its credits were billed back then.
    caplog.set_level("INFO")
    fake = FakeLlamaCloud(uploads=[_created("COMPLETED")])
    content = await _parse(pdf, fake)
    assert content.page_texts == TWO_PAGES
    assert "cache_hit=True credits=0 " in caplog.text
    assert fake.count(UPLOAD) == 1


# --- errors --------------------------------------------------------------


@pytest.mark.parametrize(
    "reply",
    [
        httpx.Response(401, json={"detail": "Invalid API key"}),
        httpx.Response(402, json=OUT_OF_CREDITS),
        httpx.Response(422, json={"detail": [{"msg": "Unsupported tier"}]}),
    ],
    ids=["bad-key", "out-of-credits", "refused-options"],
)
async def test_a_refused_upload_is_raised_at_once(pdf, reply, client_waits):
    fake = FakeLlamaCloud(uploads=[reply])
    with pytest.raises(Exception, match=f"Error code: {reply.status_code}") as info:
        await _parse(pdf, fake)
    assert fake.count(UPLOAD) == 1
    assert fake.count(JOB) == 0
    assert client_waits == []
    assert "secret-key" not in str(info.value)


async def test_out_of_credits_says_so(pdf):
    fake = FakeLlamaCloud(uploads=[httpx.Response(402, json=OUT_OF_CREDITS)])
    with pytest.raises(Exception, match="maximum number of credits"):
        await _parse(pdf, fake)


@pytest.mark.parametrize("ended", ["FAILED", "CANCELLED"])
async def test_a_job_that_ends_in_failure_is_resubmitted_fresh_then_fails(pdf, ended):
    fake = FakeLlamaCloud(polls=[_polled(ended, error="the page could not be read")])
    with pytest.raises(LlamaCloudJobFailedError, match="could not be read"):
        await _parse(pdf, fake, max_retries=2)
    assert fake.count(UPLOAD) == 2
    first, second = fake.configurations()
    assert "disable_cache" not in first
    assert second["disable_cache"] is True


async def test_an_empty_result_is_resubmitted_then_fails(pdf):
    fake = FakeLlamaCloud(polls=[_polled("COMPLETED", "   ")])
    with pytest.raises(RuntimeError, match="empty"):
        await _parse(pdf, fake, max_retries=2)
    assert fake.count(UPLOAD) == 2


async def test_a_poll_hiccup_is_retried_by_the_client(pdf, client_waits):
    fake = FakeLlamaCloud(
        polls=[
            httpx.Response(503, text="busy"),
            httpx.ConnectError("blip"),
            _polled("COMPLETED", *TWO_PAGES),
        ]
    )
    content = await _parse(pdf, fake)
    assert fake.count(UPLOAD) == 1
    assert fake.count(JOB) == 3
    assert len(client_waits) == 2
    assert "First page." in content


async def test_a_poll_failing_past_the_clients_tries_polls_the_same_job(pdf):
    tries = 1 + LLAMACLOUD_REQUEST_RETRIES
    fake = FakeLlamaCloud(
        polls=[httpx.Response(503, text="busy")] * tries
        + [_polled("COMPLETED", *TWO_PAGES)]
    )
    content = await _parse(pdf, fake)
    assert fake.count(UPLOAD) == 1
    assert fake.count(JOB) == tries + 1
    assert "Second page." in content


async def test_a_poll_refused_is_raised_at_once(pdf):
    fake = FakeLlamaCloud(polls=[httpx.Response(404, json={"detail": "no job"})])
    with pytest.raises(NotFoundError):
        await _parse(pdf, fake)
    assert fake.count(UPLOAD) == 1
    assert fake.count(JOB) == 1


async def test_a_bad_key_on_a_poll_is_raised_at_once(pdf):
    fake = FakeLlamaCloud(polls=[httpx.Response(401, json={"detail": "bad key"})])
    with pytest.raises(AuthenticationError):
        await _parse(pdf, fake)
    assert fake.count(JOB) == 1


async def test_a_refused_connection_is_retried_by_the_client_then_fails_at_once(
    pdf, client_waits
):
    # The llama-cloud client retries each request itself: 5 more tries,
    # waits of about 0.5, 1, 2, 4 and 8 s (each up to 25% shorter). Then it
    # raises APIConnectionError, and the file fails without a resubmission
    # (the guide's Limits and the parser's docstring).
    fake = FakeLlamaCloud(uploads=[httpx.ConnectError("connection refused")])
    with pytest.raises(APIConnectionError):
        await _parse(pdf, fake)
    assert fake.count(UPLOAD) == 1 + LLAMACLOUD_REQUEST_RETRIES == 6
    assert len(client_waits) == 5
    for wait, full in zip(client_waits, [0.5, 1, 2, 4, 8], strict=True):
        assert 0.75 * full <= wait <= full


@pytest.mark.parametrize(
    "first",
    [httpx.ReadError("connection reset"), httpx.Response(429, text="slow down")],
    ids=["read-error", "rate-limited"],
)
async def test_a_failed_upload_is_tried_again_by_the_client(pdf, first):
    fake = FakeLlamaCloud(uploads=[first, _created()])
    content = await _parse(pdf, fake)
    assert fake.count(UPLOAD) == 2
    assert fake.count(JOB) == 1
    assert "First page." in content


async def test_a_job_that_outlives_its_wait_is_not_resubmitted(pdf, monkeypatch):
    clock = iter(range(0, 10**7, 1000))
    monkeypatch.setattr(
        llamacloud, "time", types.SimpleNamespace(monotonic=lambda: next(clock))
    )
    fake = FakeLlamaCloud(polls=[_polled("RUNNING")])
    with pytest.raises(LlamaCloudWaitExceededError, match="Timeout while parsing"):
        await _parse(pdf, fake)
    assert fake.count(UPLOAD) == 1


# --- LlamaCloud behind the Cloud choice ----------------------------------


def _cloud_request(path, filename="paper.pdf"):
    return EtlRequest(
        file_path=str(path),
        filename=filename,
        estimated_pages=2,
        parser=DocumentParser.CLOUD,
    )


@pytest.fixture
def through_fake(monkeypatch):
    """Route the pipeline's LlamaCloud calls through ``fake``."""

    def install(fake: FakeLlamaCloud) -> FakeLlamaCloud:
        original = llamacloud.parse_with_llamacloud
        monkeypatch.setattr(
            llamacloud,
            "parse_with_llamacloud",
            functools.partial(
                original, transport=fake.transport, poll_interval=0, base_delay=0
            ),
        )
        return fake

    return install


def test_cloud_is_llamacloud_with_its_file_types():
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.service == "LLAMACLOUD"
    assert capability.available is True
    assert capability.document_extensions == LLAMAPARSE_DOCUMENT_EXTENSIONS


def test_switching_from_datalab_to_llamacloud_changes_these_file_types():
    """What an upload gains and loses when Cloud moves to LlamaCloud alone."""
    assert sorted(LLAMAPARSE_DOCUMENT_EXTENSIONS - DATALAB_DOCUMENT_EXTENSIONS) == [
        ".bmp",
        ".docm",
        ".dot",
        ".dotm",
        ".hwp",
        ".hwpx",
        ".pot",
        ".potx",
        ".pptm",
        ".rtf",
        ".svg",
        ".tif",
        ".xlsb",
        ".xlw",
    ]
    assert sorted(DATALAB_DOCUMENT_EXTENSIONS - LLAMAPARSE_DOCUMENT_EXTENSIONS) == [
        ".xltx"
    ]


async def test_a_cloud_pdf_is_read_by_llamacloud_with_its_pages(pdf, through_fake):
    fake = through_fake(FakeLlamaCloud())
    result = await EtlPipelineService().extract(_cloud_request(pdf))
    assert result.etl_service == "LLAMACLOUD"
    assert result.page_texts == list(TWO_PAGES)
    assert fake.configurations()[0]["tier"] == "agentic"
    # The conftest's key, read from the config.
    assert fake.requests[0].headers["Authorization"] == "Bearer test-key"


@pytest.fixture
def datalab_cloud(monkeypatch):
    monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", "DATALAB")


async def test_datalab_refusing_a_file_hands_it_to_llamacloud(
    datalab_cloud, pdf, through_fake, monkeypatch
):
    refusing = httpx.MockTransport(
        lambda request: httpx.Response(400, json={"detail": "corrupt"})
    )
    monkeypatch.setattr(
        datalab,
        "parse_with_datalab",
        functools.partial(
            datalab.parse_with_datalab, transport=refusing, poll_interval=0
        ),
    )
    fake = through_fake(FakeLlamaCloud())
    result = await EtlPipelineService().extract(_cloud_request(pdf))
    assert fake.count(UPLOAD) == 1
    assert "First page." in result.markdown_content


async def test_a_type_datalab_cannot_open_fails_when_llamacloud_is_out_of_credits(
    datalab_cloud, tmp_path, through_fake
):
    rtf = tmp_path / "notes.rtf"
    rtf.write_bytes(b"{\\rtf1 hello}")
    fake = through_fake(
        FakeLlamaCloud(uploads=[httpx.Response(402, json=OUT_OF_CREDITS)])
    )
    with pytest.raises(Exception, match="maximum number of credits"):
        await EtlPipelineService().extract(_cloud_request(rtf, "notes.rtf"))
    assert fake.count(UPLOAD) == 1
    assert b'filename="notes.rtf"' in fake.requests[0].content
