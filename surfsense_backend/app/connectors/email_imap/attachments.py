"""Best-effort text extraction for a small email attachment, by type.

Deliberately narrow and synchronous: this is read-only mail inspection for
one attachment the user asked to see inside a chat turn, not a document-import
pipeline, so it never goes through the full ETL job queue, its page-credit
billing, or its notifications. Everything here is capped at
:data:`MAX_ATTACHMENT_BYTES` and never raises — a failure degrades to a short
explanatory string so the chat turn always gets *something* back.

Supported today:

* ``text/plain`` — decoded directly.
* ``application/pdf`` — pymupdf4llm, with the same OCR settings the local
  document parser uses (:mod:`app.etl_pipeline.parsers.pymupdf4llm`), so a
  scanned PDF (no text layer) still yields text.
* Word (``.docx``), PowerPoint (``.pptx``) — reuse the app's existing
  ``unstructured``-backed parser (:mod:`app.etl_pipeline.parsers.unstructured`),
  the same one the ETL pipeline falls back to for these formats. No new
  dependency: ``unstructured[all-docs]`` is already a backend dependency.
* Excel (``.xlsx``/``.xls``) and CSV — read directly into a capped markdown
  table (openpyxl / xlrd / the stdlib ``csv`` module — all already installed),
  rather than unstructured's flattened cell-text, so columns stay readable.
* Images (png/jpg/webp/heic/heif) — described and OCR'd by the workspace's
  configured vision model, the same ``parse_with_vision_llm`` call a
  standalone image upload gets
  (:mod:`app.etl_pipeline.parsers.vision_llm`), metered as one
  ``vision_image`` billable action (the deployment owner's account is
  exempt, same as everywhere else vision is billed).

Not supported: legacy ``.doc`` (binary Word). ``unstructured``'s ``.doc``
partitioner shells out to LibreOffice to convert it to ``.docx`` first, and
LibreOffice is not installed in this image (only ``pandoc``, which
``unstructured`` does not use for this). Returns a clear note instead of
silently failing.

Decompression-bomb and resource guards: ``MAX_ATTACHMENT_BYTES`` only caps
the raw MIME bytes on the wire, but ``.docx``/``.xlsx``/``.pptx`` are zip
containers and PDFs can carry an unbounded page count — both can expand to
far more CPU/memory/text than the attachment's own size suggests, inline in
a chat turn's request. ``_is_zip_safe`` rejects a zip attachment before
any parser (openpyxl, unstructured) opens it, by member count, total
uncompressed size and per-member compression ratio — all read from the
zip's central directory, without decompressing anything. ``_pdf_text``
checks the page count the same way, before rendering any page.
``_cap_text`` bounds the text actually returned. Every parser that is
CPU-bound (PDF OCR/layout, openpyxl, xlrd, unstructured) runs off the event
loop via :func:`asyncio.to_thread`, under :data:`PARSE_TIMEOUT_SECONDS`.
"""

from __future__ import annotations

import asyncio
import csv
import io
import itertools
import logging
import tempfile
import zipfile
from email.message import Message
from pathlib import Path
from typing import Any

from .message_parsing import MAX_ATTACHMENT_BYTES

logger = logging.getLogger(__name__)

# Rows kept from a spreadsheet/CSV attachment, per sheet. Mail attachments
# are previewed inline in a chat turn, not imported — a multi-thousand-row
# export would blow the turn's context budget for little benefit.
MAX_TABLE_ROWS = 200

# Columns kept per row, same reasoning as MAX_TABLE_ROWS: a chat preview, not
# a data import. Also bounds the work done per row for a sheet with an
# absurd column count.
MAX_TABLE_COLS = 50

# Characters kept from any single cell, so one pathological cell cannot blow
# the row budget on its own.
MAX_CELL_CHARS = 500

# Final cap on any attachment's extracted text, applied after parsing
# (which has its own, earlier bounds — row/column/page caps, the zip-bomb
# check). This is the backstop for the cases those don't cover directly,
# such as a legitimately dense but compact PDF or docx.
MAX_EXTRACTED_TEXT_CHARS = 200_000

# .docx/.xlsx/.pptx are zip containers; a small one can still decompress to
# gigabytes. These are checked against the zip's central directory, before
# any member is decompressed.
MAX_ZIP_MEMBERS = 2_000
# Scales with MAX_ATTACHMENT_BYTES (message_parsing.py): same 10x headroom a
# legitimate large .docx/.xlsx/.pptx needs over its compressed size, now that
# the attachment cap itself is 25 MB rather than 5 MB.
MAX_ZIP_UNCOMPRESSED_BYTES = 250 * 1024 * 1024  # 250 MB
MAX_ZIP_COMPRESSION_RATIO = 100

# A PDF with an extreme page count can take arbitrarily long to lay out/OCR
# even if the file itself is small (vector content, repeated objects).
MAX_PDF_PAGES = 500

# Generous but bounded: OCR/layout on a near-the-cap PDF, or unstructured on
# a near-the-cap docx/pptx, can legitimately take tens of seconds.
PARSE_TIMEOUT_SECONDS = 60

# Mirrors app.etl_pipeline.parsers.pymupdf4llm._OCR_DPI: OCR only the regions
# the layout model flags as needing it, on top of the PDF's own text layer,
# so a scanned page (no text layer at all) is never silently dropped.
_OCR_DPI = 150

_DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
_DOC = "application/msword"
_XLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
_XLS = "application/vnd.ms-excel"
_CSV_TYPES = {"text/csv", "application/csv"}
_PPTX = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
_IMAGE_TYPES = {
    "image/png",
    "image/jpeg",
    "image/jpg",
    "image/webp",
    "image/heic",
    "image/heif",
}
_IMAGE_EXT_BY_CONTENT_TYPE = {
    "image/png": ".png",
    "image/jpeg": ".jpg",
    "image/jpg": ".jpg",
    "image/webp": ".webp",
    "image/heic": ".heic",
    "image/heif": ".heif",
}


def _too_large_note(size: int) -> str:
    return (
        f"[Attachment too large to preview "
        f"({size / (1024 * 1024):.1f} MB; limit "
        f"{MAX_ATTACHMENT_BYTES // (1024 * 1024)} MB)]"
    )


_TOO_COMPLEX_NOTE = "[Attachment too large or complex to read]"


def _cap_text(text: str | None) -> str | None:
    """The final bound on any extracted text, regardless of how it was
    produced — the backstop behind the more specific row/column/page/zip
    checks below."""
    if text is None or len(text) <= MAX_EXTRACTED_TEXT_CHARS:
        return text
    return text[:MAX_EXTRACTED_TEXT_CHARS] + "\n\n[... extracted text truncated ...]"


def _is_zip_safe(payload: bytes) -> bool:
    """True when a zip-based attachment (.docx/.xlsx/.pptx) is safe to open.

    Checked entirely from the zip's central directory — member count, total
    uncompressed size, and each member's compression ratio — without
    decompressing anything, so a crafted small file that would expand to
    gigabytes (a classic "zip bomb") is caught before any parser touches it.
    A corrupt or non-zip payload is also treated as unsafe: a real
    .docx/.xlsx/.pptx is always a valid zip, so one that isn't has already
    failed in a way a parser would fail on too.
    """
    try:
        with zipfile.ZipFile(io.BytesIO(payload)) as zf:
            infos = zf.infolist()
            if len(infos) > MAX_ZIP_MEMBERS:
                return False
            total_uncompressed = 0
            for info in infos:
                total_uncompressed += info.file_size
                if total_uncompressed > MAX_ZIP_UNCOMPRESSED_BYTES:
                    return False
                if info.compress_size > 0:
                    ratio = info.file_size / info.compress_size
                    if ratio > MAX_ZIP_COMPRESSION_RATIO:
                        return False
                elif info.file_size > 0:
                    # Claims uncompressed content from zero compressed
                    # bytes — not a shape a legitimate zip writer produces.
                    return False
    except zipfile.BadZipFile:
        return False
    return True


async def extract_attachment_text(
    msg: Message,
    filename: str,
    *,
    session: Any = None,
    workspace_id: int | None = None,
    user_id: Any = None,
) -> str | None:
    """Best-effort text for one named attachment of ``msg``, or ``None``.

    ``session``/``workspace_id``/``user_id`` are only needed to describe an
    image attachment (the workspace's vision model is billed to
    ``user_id``); every other type ignores them.
    """
    parts = msg.walk() if msg.is_multipart() else [msg]
    for part in parts:
        if (part.get_filename() or "") != filename:
            continue
        payload = part.get_payload(decode=True) or b""
        if len(payload) > MAX_ATTACHMENT_BYTES:
            return _too_large_note(len(payload))

        content_type = part.get_content_type()

        if content_type == "text/plain":
            charset = part.get_content_charset() or "utf-8"
            return payload.decode(charset, errors="replace")
        if content_type == "application/pdf":
            return await _run_sync_parser(_pdf_text, payload)
        if content_type in (_DOCX, _PPTX, _XLSX):
            # All three are zip containers; reject a decompression bomb
            # before any parser (unstructured, openpyxl) opens it.
            if not await asyncio.to_thread(_is_zip_safe, payload):
                return _TOO_COMPLEX_NOTE
            if content_type == _DOCX:
                return await _via_unstructured(payload, ".docx", filename)
            if content_type == _PPTX:
                return await _via_unstructured(payload, ".pptx", filename)
            return await _run_sync_parser(_xlsx_table, payload, filename)
        if content_type == _DOC:
            return (
                "[Legacy .doc attachments are not supported in this "
                "deployment (no document converter installed) — ask the "
                "sender for .docx or PDF]"
            )
        if content_type == _XLS:
            return await _run_sync_parser(_xls_table, payload, filename)
        if content_type in _CSV_TYPES:
            return await _run_sync_parser(_csv_table, payload, filename)
        if content_type in _IMAGE_TYPES:
            if session is None or workspace_id is None or user_id is None:
                return (
                    "[Image attachment: vision description unavailable "
                    "(no workspace context)]"
                )
            return await _vision_image_text(
                payload,
                filename,
                content_type,
                session=session,
                workspace_id=workspace_id,
                user_id=user_id,
            )
        return None
    return None


async def _run_sync_parser(func, *args: Any) -> str | None:
    """Run a synchronous, CPU-bound parser off the event loop, bounded.

    ``func`` (``_pdf_text``, ``_xlsx_table``, ``_xls_table``,
    ``_csv_table``) is fully synchronous and can be CPU-heavy (PDF
    OCR/layout, openpyxl/xlrd parsing a near-the-cap sheet); run directly on
    the event loop it would stall every other chat turn and background task
    in this worker for as long as it runs. ``asyncio.to_thread`` moves it to
    the default executor, and ``asyncio.wait_for`` bounds how long a single
    attachment can hold that thread.
    """
    try:
        return await asyncio.wait_for(
            asyncio.to_thread(func, *args), timeout=PARSE_TIMEOUT_SECONDS
        )
    except TimeoutError:
        logger.warning(
            "Attachment parsing timed out after %ss (%s)",
            PARSE_TIMEOUT_SECONDS,
            getattr(func, "__name__", func),
        )
        return _TOO_COMPLEX_NOTE


def _pdf_text(payload: bytes) -> str | None:
    try:
        import pymupdf
        import pymupdf4llm
    except ImportError:
        logger.warning("pymupdf4llm not available; cannot extract PDF attachment")
        return None
    try:
        with tempfile.NamedTemporaryFile(suffix=".pdf") as tmp:
            tmp.write(payload)
            tmp.flush()
            try:
                with pymupdf.open(tmp.name) as doc:
                    page_count = doc.page_count
            except Exception:
                logger.warning("Could not open PDF attachment", exc_info=True)
                return None
            if page_count > MAX_PDF_PAGES:
                return _TOO_COMPLEX_NOTE
            # Same OCR settings as the local document parser: a scanned
            # page (no text layer) still yields text, not an empty string.
            text = pymupdf4llm.to_markdown(
                tmp.name,
                use_ocr=True,
                force_ocr=False,
                ocr_dpi=_OCR_DPI,
                write_images=False,
                embed_images=False,
                show_progress=False,
            )
            return _cap_text(text)
    except Exception:
        logger.warning("Failed to extract PDF attachment text", exc_info=True)
        return None


async def _via_unstructured(payload: bytes, suffix: str, filename: str) -> str | None:
    """Word/PowerPoint text via the app's existing unstructured-backed parser.

    ``UnstructuredLoader.aload`` (``langchain_core.document_loaders.
    BaseLoader.aload``) already runs its synchronous parse in the default
    executor, so this is off the event loop without an explicit
    ``asyncio.to_thread`` here; ``asyncio.wait_for`` adds the same parse
    timeout the other, directly-synchronous parsers get via
    ``_run_sync_parser``.
    """
    try:
        from app.etl_pipeline.parsers.unstructured import parse_with_unstructured
    except ImportError:
        logger.warning("unstructured parser not available for %s", filename)
        return None
    try:
        with tempfile.NamedTemporaryFile(suffix=suffix) as tmp:
            tmp.write(payload)
            tmp.flush()
            text = await asyncio.wait_for(
                parse_with_unstructured(tmp.name), timeout=PARSE_TIMEOUT_SECONDS
            )
        return _cap_text(text) if text else None
    except TimeoutError:
        logger.warning(
            "Parsing %s via unstructured timed out after %ss",
            filename,
            PARSE_TIMEOUT_SECONDS,
        )
        return _TOO_COMPLEX_NOTE
    except Exception:
        logger.warning(
            "Failed to extract %s attachment text via unstructured",
            filename,
            exc_info=True,
        )
        return None


def _rows_to_markdown_table(
    header: list[str], rows: list[list[str]], *, total_rows: int
) -> str:
    lines = [
        "| " + " | ".join(header) + " |",
        "| " + " | ".join("---" for _ in header) + " |",
    ]
    lines.extend("| " + " | ".join(row) + " |" for row in rows)
    if total_rows > len(rows):
        lines.append(
            f"\n[... {total_rows - len(rows)} more row(s) omitted "
            f"(showing first {len(rows)} of {total_rows})]"
        )
    return "\n".join(lines)


def _cell_text(value: Any) -> str:
    text = "" if value is None else str(value)
    if len(text) > MAX_CELL_CHARS:
        text = text[:MAX_CELL_CHARS] + "…"
    return text


def _xlsx_table(payload: bytes, filename: str) -> str | None:
    try:
        import openpyxl
    except ImportError:
        logger.warning("openpyxl not available; cannot extract xlsx attachment")
        return None
    try:
        workbook = openpyxl.load_workbook(
            io.BytesIO(payload), read_only=True, data_only=True
        )
        sections: list[str] = []
        for sheet in workbook.worksheets:
            # ``max_row`` is read from the sheet's own dimension, already in
            # the XML — reading it costs nothing extra and avoids iterating
            # a huge sheet in full just to report its size. Rows actually
            # read are capped at MAX_TABLE_ROWS (+1 for the header) via
            # islice, so a sheet with millions of rows never has more than
            # that many pulled out of the zip.
            total_rows = max((sheet.max_row or 1) - 1, 0)
            limited = itertools.islice(
                sheet.iter_rows(values_only=True), MAX_TABLE_ROWS + 1
            )
            all_rows = [
                [_cell_text(cell) for cell in row[:MAX_TABLE_COLS]] for row in limited
            ]
            if not all_rows:
                continue
            header, *data_rows = all_rows
            sections.append(
                f"### {sheet.title}\n\n"
                + _rows_to_markdown_table(header, data_rows, total_rows=total_rows)
            )
        workbook.close()
        return _cap_text("\n\n".join(sections) if sections else None)
    except Exception:
        logger.warning("Failed to extract xlsx attachment: %s", filename, exc_info=True)
        return None


def _xls_table(payload: bytes, filename: str) -> str | None:
    try:
        import xlrd
    except ImportError:
        logger.warning("xlrd not available; cannot extract legacy .xls attachment")
        return None
    try:
        workbook = xlrd.open_workbook(file_contents=payload)
        sections: list[str] = []
        for sheet in workbook.sheets():
            if sheet.nrows == 0:
                continue
            header = [_cell_text(v) for v in sheet.row_values(0)[:MAX_TABLE_COLS]]
            last_row = min(sheet.nrows, MAX_TABLE_ROWS + 1)
            data_rows = [
                [_cell_text(v) for v in sheet.row_values(r)[:MAX_TABLE_COLS]]
                for r in range(1, last_row)
            ]
            sections.append(
                f"### {sheet.name}\n\n"
                + _rows_to_markdown_table(
                    header, data_rows, total_rows=sheet.nrows - 1
                )
            )
        return _cap_text("\n\n".join(sections) if sections else None)
    except Exception:
        logger.warning("Failed to extract xls attachment: %s", filename, exc_info=True)
        return None


def _csv_table(payload: bytes, filename: str) -> str | None:
    try:
        text = payload.decode("utf-8-sig", errors="replace")
        reader = csv.reader(io.StringIO(text))
        total_rows = 0
        kept: list[list[str]] = []
        header: list[str] | None = None
        for row in reader:
            if header is None:
                header = [_cell_text(v) for v in row[:MAX_TABLE_COLS]]
                continue
            total_rows += 1
            if len(kept) < MAX_TABLE_ROWS:
                kept.append([_cell_text(v) for v in row[:MAX_TABLE_COLS]])
        if header is None:
            return None
        return _cap_text(_rows_to_markdown_table(header, kept, total_rows=total_rows))
    except Exception:
        logger.warning("Failed to extract csv attachment: %s", filename, exc_info=True)
        return None


async def _vision_image_text(
    payload: bytes,
    filename: str,
    content_type: str,
    *,
    session: Any,
    workspace_id: int,
    user_id: Any,
) -> str:
    from app.services.billable_calls import QuotaInsufficientError
    from app.services.llm_service import get_vision_llm

    suffix = Path(filename).suffix.lower()
    if suffix not in {".png", ".jpg", ".jpeg", ".webp", ".heic", ".heif"}:
        suffix = _IMAGE_EXT_BY_CONTENT_TYPE.get(content_type, ".png")

    try:
        llm = await get_vision_llm(session, workspace_id, bill_user_id=user_id)
    except Exception:
        logger.warning("Failed to resolve vision LLM for %s", filename, exc_info=True)
        return "[Image attachment: vision model unavailable]"
    if llm is None:
        return "[Image attachment: no vision model configured for this workspace]"

    try:
        from app.etl_pipeline.parsers.vision_llm import parse_with_vision_llm

        with tempfile.NamedTemporaryFile(suffix=suffix) as tmp:
            tmp.write(payload)
            tmp.flush()
            return await parse_with_vision_llm(tmp.name, filename, llm)
    except QuotaInsufficientError:
        return (
            "[Image attachment: vision description skipped — "
            "insufficient credit for this account]"
        )
    except Exception:
        logger.warning(
            "Failed to describe image attachment %s via vision LLM",
            filename,
            exc_info=True,
        )
        return "[Image attachment: vision description failed]"
