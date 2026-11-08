from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import HTTPException
from starlette.requests import Request

from app.routes import document_files_routes


def _request(if_none_match: str | None = None, range_: str | None = None) -> Request:
    headers = []
    if if_none_match:
        headers.append((b"if-none-match", if_none_match.encode()))
    if range_:
        headers.append((b"range", range_.encode()))
    return Request({"type": "http", "method": "GET", "path": "/", "headers": headers})


def _record(*, mime_type: str = "application/pdf"):
    return SimpleNamespace(
        id=4,
        document_id=3,
        workspace_id=2,
        checksum_sha256="abc123",
        mime_type=mime_type,
        original_filename="résumé.pdf",
        storage_key="key",
        size_bytes=10,
    )


@pytest.mark.asyncio
async def test_stream_rejects_cross_workspace_access(monkeypatch):
    denied = HTTPException(status_code=403, detail="denied")
    monkeypatch.setattr(
        document_files_routes, "check_permission", AsyncMock(side_effect=denied)
    )

    with pytest.raises(HTTPException) as exc:
        await document_files_routes.stream_document_file(
            2, 3, 4, _request(), AsyncMock(), SimpleNamespace()
        )

    assert exc.value.status_code == 403


@pytest.mark.asyncio
async def test_stream_returns_404_for_mismatched_file(monkeypatch):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    session = AsyncMock()
    session.scalar.return_value = None

    with pytest.raises(HTTPException) as exc:
        await document_files_routes.stream_document_file(
            2, 3, 4, _request(), session, SimpleNamespace()
        )

    assert exc.value.status_code == 404


@pytest.mark.asyncio
async def test_stream_honors_etag(monkeypatch):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    session = AsyncMock()
    session.scalar.return_value = _record()

    response = await document_files_routes.stream_document_file(
        2, 3, 4, _request('"abc123"'), session, SimpleNamespace()
    )

    assert response.status_code == 304
    assert response.headers["etag"] == '"abc123"'
    assert response.headers["cache-control"].endswith("immutable")


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("mime_type", "mode"),
    [
        ("application/pdf", "inline"),
        ("image/png", "attachment"),
        ("text/plain", "attachment"),
        ("text/html", "attachment"),
        ("image/svg+xml", "attachment"),
        ("application/octet-stream", "attachment"),
    ],
)
async def test_stream_disposition_allowlist(monkeypatch, mime_type, mode):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    session = AsyncMock()
    session.scalar.return_value = _record(mime_type=mime_type)

    async def chunks():
        yield b"data"

    monkeypatch.setattr(
        document_files_routes, "open_document_file_stream", lambda _record: chunks()
    )
    response = await document_files_routes.stream_document_file(
        2, 3, 4, _request(), session, SimpleNamespace()
    )

    assert response.headers["content-disposition"].startswith(mode)
    assert (
        "filename*=UTF-8''r%C3%A9sum%C3%A9.pdf"
        in response.headers["content-disposition"]
    )
    assert response.headers["x-content-type-options"] == "nosniff"


def _document(*, title: str):
    return SimpleNamespace(
        id=3,
        workspace_id=2,
        title=title,
        document_type=SimpleNamespace(value="FILE"),
        status={"state": "ready"},
    )


@pytest.mark.asyncio
async def test_view_manifest_exposes_stored_original_with_canonical_mime(monkeypatch):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    original = SimpleNamespace(
        id=4,
        original_filename="budget.xlsx",
        mime_type="application/octet-stream",
        size_bytes=128,
    )
    monkeypatch.setattr(
        document_files_routes,
        "get_document_file",
        AsyncMock(return_value=original),
    )
    session = AsyncMock()
    session.scalar.return_value = _document(title="budget.xlsx")

    manifest = await document_files_routes.get_document_view_manifest(
        2, 3, session, SimpleNamespace()
    )

    assert manifest.presentation == "original"
    assert manifest.file is not None
    assert (
        manifest.file.mime_type
        == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
    )
    assert manifest.file.content_url.endswith("/documents/3/files/4/content")


@pytest.mark.asyncio
async def test_view_manifest_never_falls_back_to_text_for_missing_pdf(monkeypatch):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    monkeypatch.setattr(
        document_files_routes,
        "get_document_file",
        AsyncMock(return_value=None),
    )
    session = AsyncMock()
    session.scalar.return_value = _document(title="report.pdf")

    manifest = await document_files_routes.get_document_view_manifest(
        2, 3, session, SimpleNamespace()
    )

    assert manifest.presentation == "missing_original"
    assert manifest.file is None


@pytest.mark.asyncio
async def test_view_manifest_keeps_text_native_documents_in_editor(monkeypatch):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    monkeypatch.setattr(
        document_files_routes,
        "get_document_file",
        AsyncMock(return_value=None),
    )
    session = AsyncMock()
    session.scalar.return_value = _document(title="notes.md")

    manifest = await document_files_routes.get_document_view_manifest(
        2, 3, session, SimpleNamespace()
    )

    assert manifest.presentation == "text"


@pytest.mark.asyncio
async def test_a_range_request_returns_only_those_bytes(monkeypatch):
    """The PDF viewer loads a large source by range, page by page."""
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    session = AsyncMock()
    session.scalar.return_value = _record()
    asked = {}

    def fake_range(record, start, end):
        asked.update(start=start, end=end)

        async def gen():
            yield b"x" * (end - start + 1)

        return gen()

    monkeypatch.setattr(document_files_routes, "open_document_file_range", fake_range)
    response = await document_files_routes.stream_document_file(
        2, 3, 4, _request(range_="bytes=0-0"), session, SimpleNamespace()
    )
    assert response.status_code == 206
    assert response.headers["content-range"] == "bytes 0-0/10"
    assert response.headers["content-length"] == "1"
    assert response.headers["accept-ranges"] == "bytes"
    assert asked == {"start": 0, "end": 0}


@pytest.mark.asyncio
async def test_an_unsatisfiable_range_is_416(monkeypatch):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    session = AsyncMock()
    session.scalar.return_value = _record()
    response = await document_files_routes.stream_document_file(
        2, 3, 4, _request(range_="bytes=50-60"), session, SimpleNamespace()
    )
    assert response.status_code == 416
    assert response.headers["content-range"] == "bytes */10"


@pytest.mark.asyncio
async def test_a_full_request_advertises_ranges_and_length(monkeypatch):
    monkeypatch.setattr(document_files_routes, "check_permission", AsyncMock())
    session = AsyncMock()
    session.scalar.return_value = _record()

    async def gen():
        yield b"0123456789"

    monkeypatch.setattr(document_files_routes, "open_document_file_stream", lambda _r: gen())
    response = await document_files_routes.stream_document_file(
        2, 3, 4, _request(), session, SimpleNamespace()
    )
    assert response.status_code == 200
    assert response.headers["accept-ranges"] == "bytes"
    assert response.headers["content-length"] == "10"
