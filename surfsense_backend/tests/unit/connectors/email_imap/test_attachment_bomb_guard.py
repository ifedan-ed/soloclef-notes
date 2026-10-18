"""Decompression-bomb and resource guards for email attachment parsing.

``_is_zip_safe`` is exercised directly (it only needs bytes, no IMAP/email
plumbing); the end-to-end "zip bomb attachment is refused with a clear note"
path goes through ``extract_attachment_text`` with a real crafted zip, the
same way the GreenMail integration tests exercise the happy path.
"""

from __future__ import annotations

import io
import zipfile
from email.message import EmailMessage

import pytest

from app.connectors.email_imap import attachments

pytestmark = pytest.mark.unit


def _normal_docx_zip() -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("[Content_Types].xml", "<Types/>")
        zf.writestr("word/document.xml", "<w:document/>")
    return buf.getvalue()


def _zip_bomb(*, member_size: int = 50 * 1024 * 1024, members: int = 1) -> bytes:
    """A small zip that claims to decompress to ``member_size`` per member.

    Highly compressible content (all zero bytes) makes the compression ratio
    extreme while the compressed size on the wire stays tiny — the actual
    shape a decompression bomb takes.
    """
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        for i in range(members):
            zf.writestr(f"word/bomb{i}.xml", b"\x00" * member_size)
    return buf.getvalue()


def test_a_normal_small_docx_zip_is_safe():
    assert attachments._is_zip_safe(_normal_docx_zip()) is True


def test_a_zip_with_extreme_compression_ratio_is_unsafe():
    assert attachments._is_zip_safe(_zip_bomb(member_size=50 * 1024 * 1024)) is False


def test_a_zip_exceeding_total_uncompressed_size_is_unsafe():
    # Many members, each individually under the ratio limit, that together
    # exceed the total uncompressed budget.
    payload = _zip_bomb(member_size=2 * 1024 * 1024, members=40)
    assert attachments._is_zip_safe(payload) is False


def test_a_zip_with_too_many_members_is_unsafe():
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_STORED) as zf:
        for i in range(attachments.MAX_ZIP_MEMBERS + 1):
            zf.writestr(f"f{i}.txt", "x")
    assert attachments._is_zip_safe(buf.getvalue()) is False


def test_not_a_zip_at_all_is_unsafe():
    assert attachments._is_zip_safe(b"not a zip file") is False


async def test_docx_zip_bomb_attachment_returns_a_clear_note_not_a_crash():
    msg = EmailMessage()
    msg["From"] = "a@example.com"
    msg["Subject"] = "Bomb"
    msg.set_content("See attached.")
    msg.add_attachment(
        _zip_bomb(member_size=50 * 1024 * 1024),
        maintype="application",
        subtype="vnd.openxmlformats-officedocument.wordprocessingml.document",
        filename="bomb.docx",
    )
    out = await attachments.extract_attachment_text(msg, "bomb.docx")
    assert out == attachments._TOO_COMPLEX_NOTE


async def test_pdf_with_too_many_pages_returns_a_clear_note():
    import pymupdf

    doc = pymupdf.open()
    for _ in range(attachments.MAX_PDF_PAGES + 1):
        doc.new_page()
    pdf_bytes = doc.tobytes()
    doc.close()

    msg = EmailMessage()
    msg["From"] = "a@example.com"
    msg["Subject"] = "Huge PDF"
    msg.set_content("See attached.")
    msg.add_attachment(
        pdf_bytes, maintype="application", subtype="pdf", filename="huge.pdf"
    )
    out = await attachments.extract_attachment_text(msg, "huge.pdf")
    assert out == attachments._TOO_COMPLEX_NOTE


def test_cap_text_truncates_past_the_limit():
    long_text = "x" * (attachments.MAX_EXTRACTED_TEXT_CHARS + 1000)
    capped = attachments._cap_text(long_text)
    assert capped is not None
    assert len(capped) <= attachments.MAX_EXTRACTED_TEXT_CHARS + len(
        "\n\n[... extracted text truncated ...]"
    )
    assert capped.endswith("[... extracted text truncated ...]")


def test_cap_text_leaves_short_text_untouched():
    assert attachments._cap_text("short") == "short"
    assert attachments._cap_text(None) is None


async def test_csv_table_bounds_rows_cols_and_cell_length():
    huge_row_count = attachments.MAX_TABLE_ROWS + 50
    huge_col_count = attachments.MAX_TABLE_COLS + 20
    header = ",".join(f"c{i}" for i in range(huge_col_count))
    long_cell = "y" * (attachments.MAX_CELL_CHARS + 100)
    rows = [f"{long_cell}," + ",".join("v" for _ in range(huge_col_count - 1))] * huge_row_count
    csv_bytes = (header + "\n" + "\n".join(rows) + "\n").encode()

    out = attachments._csv_table(csv_bytes, "big.csv")
    assert out is not None
    # Column cap: header line keeps only MAX_TABLE_COLS columns.
    assert out.splitlines()[0].count("|") - 1 == attachments.MAX_TABLE_COLS
    # Row cap: omission note present and mentions the real total.
    assert f"of {huge_row_count}" in out
    # Cell cap: the long cell was truncated, not reproduced in full.
    assert long_cell not in out
