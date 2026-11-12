"""Test documents, built here so no real file is ever checked in."""

from __future__ import annotations

from pathlib import Path

import pymupdf


def text_pdf(path: Path, pages: int = 2) -> Path:
    """A PDF with a real text layer: a heading and a sentence per page."""
    document = pymupdf.open()
    for number in range(1, pages + 1):
        page = document.new_page()
        page.insert_text((72, 100), f"Chapter {number}", fontsize=24)
        page.insert_text((72, 150), f"The body of page {number} is here.", fontsize=11)
    document.save(path)
    return path


def scanned_pdf(path: Path) -> Path:
    """A PDF whose only page is a picture, as a scanner produces."""
    document = pymupdf.open()
    page = document.new_page()
    pixmap = pymupdf.Pixmap(pymupdf.csRGB, pymupdf.IRect(0, 0, 200, 200))
    pixmap.clear_with(200)
    page.insert_image(page.rect, pixmap=pixmap)
    document.save(path)
    return path
