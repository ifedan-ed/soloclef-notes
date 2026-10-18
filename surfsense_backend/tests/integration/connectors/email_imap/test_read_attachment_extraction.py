"""Attachment extraction for the Email (IMAP) connector, against real GreenMail.

Each attachment is actually delivered over SMTP and fetched back over IMAP
(real wire protocol, nothing mocked except the vision LLM for images), then
run through ``service.read_attachment_text`` — the same path
``read_email(..., attachment_filename=...)`` uses in a chat turn.
"""

from __future__ import annotations

import io
import uuid
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest

from app.connectors.email_imap import service

from .conftest import ALICE, deliver_message, requires_greenmail

pytestmark = [pytest.mark.integration, requires_greenmail]


async def _deliver_and_read(
    db_session, owner, imap_settings, *, attachment: tuple[str, bytes, str]
) -> str | None:
    account = await service.add_account(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        label="Alice",
        host=imap_settings["host"],
        port=imap_settings["port"],
        use_tls=imap_settings["use_tls"],
        username=ALICE.username,
        app_password=ALICE.password,
    )
    filename, data, content_type = attachment
    token = uuid.uuid4().hex[:12]
    deliver_message(
        ALICE.email,
        subject=f"Attachment test {token}",
        body="See attached.",
        attachment=attachment,
    )

    results = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="INBOX",
        text=None,
        from_=None,
        to=None,
        subject=token,
        since=None,
        before=None,
        limit=5,
    )
    assert results, "expected the delivered message to be found"
    uid = results[0]["uid"]

    return await service.read_attachment_text(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="INBOX",
        uid=uid,
        filename=filename,
    )


def _docx_bytes(text: str) -> bytes:
    from docx import Document

    doc = Document()
    doc.add_paragraph(text)
    buf = io.BytesIO()
    doc.save(buf)
    return buf.getvalue()


def _pptx_bytes(title: str, body: str) -> bytes:
    from pptx import Presentation

    pres = Presentation()
    slide = pres.slides.add_slide(pres.slide_layouts[1])
    slide.shapes.title.text = title
    slide.placeholders[1].text = body
    buf = io.BytesIO()
    pres.save(buf)
    return buf.getvalue()


def _xlsx_bytes(rows: list[list]) -> bytes:
    import openpyxl

    wb = openpyxl.Workbook()
    ws = wb.active
    for row in rows:
        ws.append(row)
    buf = io.BytesIO()
    wb.save(buf)
    return buf.getvalue()


def _xls_bytes() -> bytes:
    """A minimal real .xls fixture (Plan/Monthly, Silver/29.99).

    No .xls *writer* is available in this image (only the ``xlrd`` reader,
    which production code uses) and adding one just to generate a test
    fixture isn't worth a new dependency — this file was generated once,
    offline, and is read back verbatim.
    """
    return (
        Path(__file__).parent / "fixtures" / "sample_quote.xls"
    ).read_bytes()


def _png_bytes() -> bytes:
    from PIL import Image

    buf = io.BytesIO()
    Image.new("RGB", (32, 32), (10, 20, 30)).save(buf, format="PNG")
    return buf.getvalue()


async def test_docx_attachment_text_is_extracted(db_session, make_owner, imap_settings):
    owner = await make_owner()
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=(
            "policy.docx",
            _docx_bytes("Term life insurance quote: $42.50 per month."),
            "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        ),
    )
    assert out is not None
    assert "42.50" in out


async def test_pptx_attachment_text_is_extracted(db_session, make_owner, imap_settings):
    owner = await make_owner()
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=(
            "deck.pptx",
            _pptx_bytes("Coverage Options", "Gold plan: $99.99/mo"),
            "application/vnd.openxmlformats-officedocument.presentationml.presentation",
        ),
    )
    assert out is not None
    assert "Coverage Options" in out
    assert "99.99" in out


async def test_xlsx_attachment_is_a_capped_markdown_table(
    db_session, make_owner, imap_settings
):
    owner = await make_owner()
    rows = [["Plan", "Monthly"]] + [[f"Plan {i}", i] for i in range(250)]
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=(
            "quotes.xlsx",
            _xlsx_bytes(rows),
            "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        ),
    )
    assert out is not None
    assert "| Plan | Monthly |" in out
    assert "Plan 0" in out
    # 250 data rows, capped at 200 -> 50 omitted, and row 249 must not appear.
    assert "more row(s) omitted" in out
    assert "Plan 249" not in out


async def test_legacy_xls_attachment_is_a_markdown_table(
    db_session, make_owner, imap_settings
):
    owner = await make_owner()
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=(
            "quote.xls",
            _xls_bytes(),
            "application/vnd.ms-excel",
        ),
    )
    assert out is not None
    assert "Silver" in out
    assert "29.99" in out


async def test_csv_attachment_is_a_markdown_table(db_session, make_owner, imap_settings):
    owner = await make_owner()
    csv_bytes = b"Plan,Monthly\nBronze,19.99\nGold,59.99\n"
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=("quote.csv", csv_bytes, "text/csv"),
    )
    assert out is not None
    assert "| Plan | Monthly |" in out
    assert "Bronze" in out
    assert "59.99" in out


async def test_legacy_doc_attachment_returns_a_clear_unsupported_note(
    db_session, make_owner, imap_settings
):
    owner = await make_owner()
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=("old.doc", b"not a real doc, just bytes", "application/msword"),
    )
    assert out is not None
    assert "not supported" in out.lower()
    assert ".docx" in out


async def test_oversized_attachment_is_skipped_with_a_clear_note(
    db_session, make_owner, imap_settings
):
    owner = await make_owner()
    big = b"x" * (6 * 1024 * 1024)
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=("huge.txt", big, "text/plain"),
    )
    assert out is not None
    assert "too large" in out.lower()
    assert "6.0 MB" in out


async def test_image_attachment_is_described_by_the_vision_model(
    db_session, make_owner, imap_settings, monkeypatch
):
    """The vision LLM call is mocked (no real model call in tests); the
    attachment delivery/fetch/decode path is real GreenMail wire protocol."""
    from app.connectors.email_imap import attachments as attachments_module

    fake_response = MagicMock()
    fake_response.content = "A small dark blue square."
    fake_llm = AsyncMock()
    fake_llm.ainvoke.return_value = fake_response

    async def _fake_get_vision_llm(session, workspace_id, **kwargs):
        return fake_llm

    monkeypatch.setattr(
        "app.services.llm_service.get_vision_llm", _fake_get_vision_llm
    )

    owner = await make_owner()
    out = await _deliver_and_read(
        db_session,
        owner,
        imap_settings,
        attachment=("photo.png", _png_bytes(), "image/png"),
    )
    assert out == "A small dark blue square."
    fake_llm.ainvoke.assert_awaited_once()
    assert attachments_module  # module imported for the monkeypatch target's sake
