"""Word and PowerPoint mail attachments still yield their text.

Both are read by the same local parser a .pdf attachment gets
(:mod:`app.etl_pipeline.parsers.pymupdf4llm`'s MuPDF backend, which opens
.docx and .pptx directly), so no cloud call and no extra dependency is
needed to preview one inside a chat turn.
"""

from __future__ import annotations

import io
from email.message import EmailMessage

import pytest

from app.connectors.email_imap import attachments

pytestmark = pytest.mark.unit

_DOCX_CONTENT_TYPE = (
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
)
_PPTX_CONTENT_TYPE = (
    "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)


def _docx_bytes(text: str) -> bytes:
    import docx

    buf = io.BytesIO()
    document = docx.Document()
    document.add_paragraph(text)
    document.save(buf)
    return buf.getvalue()


def _pptx_bytes(text: str) -> bytes:
    from pptx import Presentation

    buf = io.BytesIO()
    presentation = Presentation()
    slide = presentation.slides.add_slide(presentation.slide_layouts[1])
    slide.shapes.title.text = text
    presentation.save(buf)
    return buf.getvalue()


async def _extract(filename: str, payload: bytes, content_type: str) -> str | None:
    msg = EmailMessage()
    msg["From"] = "a@example.com"
    msg["Subject"] = "Attachment"
    msg.set_content("See attached.")
    maintype, _, subtype = content_type.partition("/")
    msg.add_attachment(payload, maintype=maintype, subtype=subtype, filename=filename)
    return await attachments.extract_attachment_text(msg, filename)


async def test_a_docx_attachment_yields_its_body_text():
    out = await _extract(
        "notes.docx", _docx_bytes("DOCX_BODY_MARKER"), _DOCX_CONTENT_TYPE
    )
    assert out is not None
    assert "DOCX_BODY_MARKER" in out


async def test_a_pptx_attachment_yields_its_slide_text():
    out = await _extract(
        "deck.pptx", _pptx_bytes("PPTX_TITLE_MARKER"), _PPTX_CONTENT_TYPE
    )
    assert out is not None
    assert "PPTX_TITLE_MARKER" in out


async def test_a_corrupt_office_attachment_is_refused_not_crashed():
    """A zip that is not a real .docx fails the bomb guard's validity check."""
    out = await _extract("broken.docx", b"not a zip at all", _DOCX_CONTENT_TYPE)
    assert out == attachments._TOO_COMPLEX_NOTE
