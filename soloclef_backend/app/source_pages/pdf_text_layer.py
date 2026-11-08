"""Read a PDF's own text layer, page by page, with no OCR and no model."""

from __future__ import annotations

import logging

logger = logging.getLogger(__name__)


def read_pdf_page_texts(data: bytes) -> list[str]:
    """Each page's embedded text, in page order (page ``i + 1`` at index ``i``).

    A scanned page with no text layer comes back as an empty string. Returns
    an empty list when the bytes are not a readable PDF.
    """
    import pymupdf

    try:
        document = pymupdf.open(stream=data, filetype="pdf")
    except Exception:
        logger.warning("Could not open PDF to read its text layer", exc_info=True)
        return []
    with document:
        texts: list[str] = []
        for page in document:
            try:
                texts.append(page.get_text("text"))
            except Exception:
                texts.append("")
        return texts
