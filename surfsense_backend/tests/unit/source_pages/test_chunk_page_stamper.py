"""Stamping chunk rows with pages, on upload and in the backfill."""

from contextlib import asynccontextmanager
from types import SimpleNamespace

import pytest

from app.source_pages import backfill as backfill_module, chunk_page_stamper as stamper
from app.source_pages.chunk_page_stamper import StampOutcome
from app.source_pages.pdf_text_layer import read_pdf_page_texts

pytestmark = pytest.mark.unit

PAGE_SENTENCES = [
    "Kawasaki disease causes fever for at least five days in young children.",
    "Intravenous immunoglobulin is given within ten days of fever onset.",
    "Echocardiography screens for coronary artery aneurysms at diagnosis.",
]


def _pdf_bytes(pages: list[str]) -> bytes:
    pymupdf = pytest.importorskip("pymupdf")
    document = pymupdf.open()
    for text in pages:
        document.new_page().insert_textbox(pymupdf.Rect(72, 72, 540, 720), text)
    data = document.tobytes()
    document.close()
    return data


class _Rows:
    def __init__(self, rows):
        self._rows = rows

    def all(self):
        return self._rows


class _FakeSession:
    """Answers the stamper's one SELECT and records its UPDATE."""

    def __init__(self, rows):
        self.rows = rows
        self.updates = None
        self.commits = 0
        self.rollbacks = 0

    async def execute(self, statement, params=None):
        if params is not None:
            self.updates = params
            return None
        return _Rows(self.rows)

    async def commit(self):
        self.commits += 1

    async def rollback(self):
        self.rollbacks += 1

    @asynccontextmanager
    async def begin_nested(self):
        yield


def _row(chunk_id, content, page_start=None, page_end=None):
    return SimpleNamespace(
        id=chunk_id, content=content, page_start=page_start, page_end=page_end
    )


def _pdf_original():
    return SimpleNamespace(mime_type="application/pdf", original_filename="kd.pdf")


def test_the_text_layer_reads_one_string_per_page():
    texts = read_pdf_page_texts(_pdf_bytes(PAGE_SENTENCES))

    assert len(texts) == 3
    assert "immunoglobulin" in texts[1]


def test_bytes_that_are_not_a_pdf_have_no_text_layer():
    assert read_pdf_page_texts(b"not a pdf") == []


async def test_chunks_are_stamped_from_the_stored_pdf_text_layer(mocker):
    data = _pdf_bytes(PAGE_SENTENCES)

    async def _stream(_record):
        yield data[:100]
        yield data[100:]

    mocker.patch.object(stamper, "open_document_file_stream", new=_stream)
    session = _FakeSession(
        [
            _row(
                10,
                "## Presentation\n\nKawasaki disease causes **fever** for at least five days",
            ),
            _row(
                11,
                "Echocardiography screens for coronary artery aneurysms at diagnosis.",
            ),
            _row(12, "Intravenous immunoglobulin is given within ten days", 2, 2),
        ]
    )

    outcome = await stamper.stamp_chunk_pages(session, 5, original=_pdf_original())

    assert outcome == StampOutcome(5, chunks=3, placed=3, changed=2)
    # Row 12 already had the right page, so it is not rewritten.
    assert session.updates == [
        {"id": 10, "page_start": 1, "page_end": 1},
        {"id": 11, "page_start": 3, "page_end": 3},
    ]


async def test_parser_pages_are_used_without_reading_the_original(mocker):
    stream = mocker.patch.object(stamper, "open_document_file_stream")
    session = _FakeSession([_row(1, PAGE_SENTENCES[2])])

    outcome = await stamper.stamp_chunk_pages(
        session,
        5,
        parser_page_texts=PAGE_SENTENCES,
        original=_pdf_original(),
    )

    assert outcome.placed == 1
    assert session.updates == [{"id": 1, "page_start": 3, "page_end": 3}]
    stream.assert_not_called()


async def test_a_document_whose_original_is_not_a_pdf_is_left_alone(mocker):
    session = _FakeSession([_row(1, "text")])
    original = SimpleNamespace(
        mime_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        original_filename="notes.docx",
    )

    outcome = await stamper.stamp_chunk_pages(session, 5, original=original)

    assert outcome.skipped == "no_pdf_original"
    assert session.updates is None


async def test_a_legacy_pdf_row_without_a_mime_type_is_still_a_pdf():
    assert stamper.is_pdf(SimpleNamespace(mime_type=None, original_filename="A.PDF"))


async def test_a_scanned_pdf_with_no_text_anywhere_is_reported_not_written(mocker):
    data = _pdf_bytes(["", ""])

    async def _stream(_record):
        yield data

    mocker.patch.object(stamper, "open_document_file_stream", new=_stream)
    session = _FakeSession([_row(1, "some chunk of OCR text from a scan")])

    outcome = await stamper.stamp_chunk_pages(session, 5, original=_pdf_original())

    assert outcome.skipped == "no_page_text"
    assert session.updates is None


async def test_stamping_after_upload_never_raises(mocker):
    mocker.patch.object(
        stamper, "stamp_chunk_pages", side_effect=RuntimeError("storage down")
    )
    session = _FakeSession([])

    await stamper.stamp_after_upload(session, 5, None)

    assert session.commits == 0


async def test_stamping_after_upload_commits(mocker):
    mocker.patch.object(
        stamper,
        "stamp_chunk_pages",
        new=mocker.AsyncMock(return_value=StampOutcome(5, chunks=2, placed=2)),
    )
    session = _FakeSession([])

    await stamper.stamp_after_upload(session, 5, ["page"])

    assert session.commits == 1
    stamper.stamp_chunk_pages.assert_awaited_once_with(
        session, 5, parser_page_texts=["page"]
    )


async def test_backfill_dry_run_writes_nothing(mocker):
    mocker.patch.object(
        backfill_module,
        "documents_needing_pages",
        new=mocker.AsyncMock(return_value=[1, 2]),
    )
    mocker.patch.object(
        backfill_module, "get_document_file", new=mocker.AsyncMock(return_value=None)
    )
    mocker.patch.object(
        backfill_module,
        "stamp_chunk_pages",
        new=mocker.AsyncMock(
            side_effect=[
                StampOutcome(1, chunks=4, placed=3, changed=3),
                StampOutcome(2, chunks=2, skipped="no_page_text"),
            ]
        ),
    )
    session = _FakeSession([])

    report = await backfill_module.backfill_chunk_pages(session, apply=False)

    assert session.commits == 0
    assert session.rollbacks == 2
    assert (report.documents, report.stamped, report.placed, report.changed) == (
        2,
        1,
        3,
        3,
    )
    assert report.skipped == {"no_page_text": 1}


async def test_backfill_apply_commits_each_document_and_survives_a_failure(mocker):
    mocker.patch.object(
        backfill_module,
        "documents_needing_pages",
        new=mocker.AsyncMock(return_value=[1, 2, 3]),
    )
    mocker.patch.object(
        backfill_module, "get_document_file", new=mocker.AsyncMock(return_value=None)
    )
    mocker.patch.object(
        backfill_module,
        "stamp_chunk_pages",
        new=mocker.AsyncMock(
            side_effect=[
                StampOutcome(1, chunks=1, placed=1, changed=1),
                RuntimeError("blob missing"),
                StampOutcome(3, chunks=1, placed=1, changed=1),
            ]
        ),
    )
    seen = []
    session = _FakeSession([])

    report = await backfill_module.backfill_chunk_pages(
        session, apply=True, on_document=seen.append
    )

    assert session.commits == 2
    assert report.failed == 1
    assert report.stamped == 2
    assert [o.document_id for o in seen] == [1, 3]
