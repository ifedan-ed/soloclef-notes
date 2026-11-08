"""A shared Studio output carries every file its owner's viewer reads.

The public page used to get the main file alone, so a slide deck's PDF
preview could not be reached and each kind fell back to a generic page.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest
from starlette.requests import Request

import app.services.public_chat_service as public_chat_service
from app.artifacts.persistence import ArtifactFileRole
from app.public_sharing.output_files import shared_file_response

pytestmark = pytest.mark.unit

TOKEN = "tok"


def _file(file_id, role, filename, mime, size=10):
    return SimpleNamespace(
        id=file_id,
        role=role,
        original_filename=filename,
        mime_type=mime,
        size_bytes=size,
        storage_key=f"secret/{file_id}",
        storage_backend="local",
        checksum_sha256=f"sum{file_id}",
    )


DECK = _file(
    21,
    ArtifactFileRole.PRIMARY,
    "planets.pptx",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation",
)
PREVIEW = _file(20, ArtifactFileRole.PREVIEW, "planets.pdf", "application/pdf")


def _output_snapshot(artifact_ids=(114,)):
    return SimpleNamespace(
        notebook_output_id=78,
        expires_at=None,
        snapshot_data={
            "kind": "output",
            "title": "Planets",
            "snapshot_at": datetime.now(UTC).isoformat(),
            "artifact_ids": list(artifact_ids),
            "output": {
                "title": "Planets",
                "kind": "slides",
                "format": "pptx",
                "markdown": "# Planets",
                "artifact_id": 114,
                "file": {
                    "filename": DECK.original_filename,
                    "mime_type": DECK.mime_type,
                    "size_bytes": 10,
                    "display": "file",
                },
            },
        },
    )


def _session(files, generation=3):
    session = AsyncMock()
    session.scalar.return_value = SimpleNamespace(id=114, generation=generation)
    scalars = MagicMock()
    scalars.all.return_value = files
    session.scalars.return_value = scalars
    return session


@pytest.mark.asyncio
async def test_a_shared_deck_lists_its_pdf_preview_with_this_links_urls(monkeypatch):
    async def row(*_args):
        return _output_snapshot()

    monkeypatch.setattr(public_chat_service, "_snapshot_row", row)

    page = await public_chat_service.get_public_chat(_session([PREVIEW, DECK]), TOKEN)

    output = page["output"]
    assert output["generation"] == 3
    assert [(f["role"], f["filename"]) for f in output["files"]] == [
        ("primary", "planets.pptx"),
        ("preview", "planets.pdf"),
    ]
    assert output["files"][1]["content_url"] == (
        "/api/v1/public/tok/artifacts/114/files/20/content"
    )
    # The main file stays for apps made before ``files``.
    assert output["file"]["filename"] == "planets.pptx"
    assert "storage_key" not in str(output)


@pytest.mark.asyncio
async def test_an_output_the_snapshot_does_not_reference_lists_no_files(monkeypatch):
    async def row(*_args):
        return _output_snapshot(artifact_ids=())

    monkeypatch.setattr(public_chat_service, "_snapshot_row", row)
    session = _session([PREVIEW, DECK])

    page = await public_chat_service.get_public_chat(session, TOKEN)

    assert page["output"]["files"] == []
    session.scalars.assert_not_called()


@pytest.fixture
def live_snapshot(monkeypatch):
    async def fake(*_args, **_kwargs):
        return _output_snapshot()

    monkeypatch.setattr(public_chat_service, "get_snapshot_by_token", fake)


@pytest.mark.asyncio
async def test_any_file_of_the_shared_artifact_is_served(live_snapshot):
    session = AsyncMock()
    session.scalar.return_value = PREVIEW

    served = await public_chat_service.get_snapshot_artifact_file_by_id(
        session, TOKEN, 114, 20
    )

    assert served is PREVIEW


@pytest.mark.asyncio
async def test_another_artifacts_file_is_refused_without_a_query(live_snapshot):
    session = AsyncMock()

    served = await public_chat_service.get_snapshot_artifact_file_by_id(
        session, TOKEN, 999, 20
    )

    assert served is None
    session.scalar.assert_not_called()


@pytest.mark.asyncio
async def test_an_expired_link_serves_no_file(monkeypatch):
    expired = _output_snapshot()
    expired.expires_at = datetime.now(UTC) - timedelta(days=1)

    async def row(*_args):
        return expired

    monkeypatch.setattr(public_chat_service, "_snapshot_row", row)
    session = AsyncMock()

    served = await public_chat_service.get_snapshot_artifact_file_by_id(
        session, TOKEN, 114, 20
    )

    assert served is None


def _request(headers=None):
    raw = [(k.lower().encode(), v.encode()) for k, v in (headers or {}).items()]
    return Request({"type": "http", "method": "GET", "headers": raw, "path": "/"})


@pytest.mark.parametrize(
    ("record", "sandboxed"),
    [
        (PREVIEW, False),
        (DECK, True),
        (_file(5, ArtifactFileRole.PRIMARY, "deck.json", "application/json"), True),
        (_file(6, ArtifactFileRole.PRIMARY, "report.html", "text/html"), True),
    ],
)
def test_shared_files_revalidate_and_only_pdf_or_mp4_run_inline(
    monkeypatch, record, sandboxed
):
    import app.routes.artifacts_routes as artifacts_routes

    monkeypatch.setattr(
        artifacts_routes, "open_artifact_file_stream", lambda _r: iter([b"x"])
    )

    response = shared_file_response(record, _request())

    assert response.headers["cache-control"] == "private, no-cache"
    assert ("content-security-policy" in response.headers) is sandboxed
    assert response.headers["accept-ranges"] == "bytes"
