"""The service end to end, in process: a real server, real workers, real parser."""

import json
import threading
import urllib.error
import urllib.request
from urllib.parse import quote

import pytest

from parse_service.convert import to_pages
from parse_service.http_api import ParseServer
from parse_service.workers import WorkerPool
from tests.documents import scanned_pdf, text_pdf

MAX_BYTES = 256 * 1024


@pytest.fixture(scope="module")
def base_url():
    pool = WorkerPool(
        size=1, convert=to_pages, parse_timeout_seconds=120, queue_wait_seconds=30
    )
    server = ParseServer(("127.0.0.1", 0), pool, MAX_BYTES)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{server.server_address[1]}"
    server.shutdown()
    server.server_close()
    pool.close()


def call(request):
    """(status, decoded JSON body) for a request, whatever the status."""
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        return error.code, json.load(error)


def parse(base_url, filename, body):
    return call(
        urllib.request.Request(
            f"{base_url}/v1/parse?filename={quote(filename)}", data=body, method="POST"
        )
    )


def test_health_names_the_parser_and_its_pinned_version(base_url):
    status, body = call(f"{base_url}/health")

    assert status == 200
    assert body["status"] == "ok"
    assert body["parser"] == "pymupdf4llm"
    assert body["parser_version"] == "1.28.2"
    assert body["workers"] == body["workers_alive"] == 1


def test_a_pdf_comes_back_as_markdown_pages(base_url, tmp_path):
    pdf = text_pdf(tmp_path / "book.pdf", pages=2).read_bytes()

    status, body = parse(base_url, "My book (final).pdf", pdf)

    assert status == 200
    assert body["parser_version"] == "1.28.2"
    assert [page["number"] for page in body["pages"]] == [1, 2]
    assert "Chapter 1" in body["pages"][0]["markdown"]


def test_a_scan_is_refused_with_its_kind(base_url, tmp_path):
    status, body = parse(
        base_url, "scan.pdf", scanned_pdf(tmp_path / "scan.pdf").read_bytes()
    )

    assert status == 422
    assert body["error"]["kind"] == "no_text_layer"


def test_a_damaged_file_is_a_parse_error(base_url):
    status, body = parse(base_url, "damaged.pdf", b"this is not a PDF")

    assert status == 422
    assert body["error"]["kind"] == "parse_error"


def test_an_unsupported_extension_is_refused_before_the_body_is_read(base_url):
    status, body = parse(base_url, "sheet.xlsx", b"PK")

    assert status == 415
    assert body["error"]["kind"] == "unsupported_format"
    assert ".docx" in body["error"]["message"]


def test_a_file_over_the_limit_is_refused(base_url):
    status, body = parse(base_url, "big.pdf", b"x" * (MAX_BYTES + 1))

    assert status == 413
    assert body["error"]["kind"] == "too_large"


def test_the_filename_is_required(base_url):
    status, body = call(
        urllib.request.Request(f"{base_url}/v1/parse", data=b"x", method="POST")
    )

    assert status == 400
    assert body["error"]["kind"] == "bad_request"


def test_an_unknown_path_is_not_found(base_url):
    status, body = call(f"{base_url}/nothing")

    assert status == 404
    assert body["error"]["kind"] == "not_found"
