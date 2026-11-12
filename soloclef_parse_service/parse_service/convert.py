"""One file to Markdown pages with pymupdf4llm. Runs inside a worker process."""

from __future__ import annotations

from pathlib import PurePosixPath
from typing import Final

from parse_service.refusal import NO_TEXT_LAYER, PARSE_ERROR, refusal

PARSER_NAME: Final = "pymupdf4llm"

#: The formats MuPDF both opens and lays out as text. Images and SVG are left
#: out although MuPDF opens them: with OCR off they come back blank, and a
#: format that can only ever be refused should be refused by name, up front.
#: .xlsx is out because MuPDF's spreadsheet reader drops every string cell.
SUPPORTED_EXTENSIONS: Final = frozenset({".pdf", ".docx", ".pptx", ".epub"})


def extension_of(filename: str) -> str:
    return PurePosixPath(filename).suffix.lower()


def parser_version() -> str:
    from importlib.metadata import version

    return version(PARSER_NAME)


def to_pages(path: str) -> dict:
    """``{"pages": [{"number", "markdown"}]}`` for the file at ``path``, or a refusal.

    The document's Markdown is the pages' Markdown concatenated in order with
    nothing between them; that is exactly pymupdf4llm's single-string output,
    so the caller can keep page offsets without searching for them.
    """
    import pymupdf4llm  # heavy (about 2 s); the worker imports it once, at start

    try:
        chunks = pymupdf4llm.to_markdown(
            path,
            # Pinned off, not left to the library's default: a release that
            # flips the default must not put CPU OCR back on the ingest path.
            use_ocr=False,
            # The Markdown is chunked and embedded. Image files on disk would
            # be litter, and base64 in the text would be embedded as noise.
            write_images=False,
            embed_images=False,
            show_progress=False,
            # One entry per page, so every page's Markdown is known.
            page_chunks=True,
        )
    except Exception as exc:
        # Whatever MuPDF raises on a damaged or encrypted file is about this
        # file, so it is a refusal and not a server error.
        return refusal(PARSE_ERROR, f"{type(exc).__name__}: {exc}")

    pages = [
        # pymupdf4llm numbers pages from 1 in metadata.page_number. Read
        # strictly: a release that renames the key should fail loudly here,
        # not shift every citation by guessing from the list position.
        {"number": int(chunk["metadata"]["page_number"]), "markdown": chunk["text"] or ""}
        for chunk in chunks
    ]
    if not any(page["markdown"].strip() for page in pages):
        return refusal(
            NO_TEXT_LAYER,
            "No page has any text. This looks like a scan: pages of images "
            "with no text layer. OCR is not run here.",
        )
    return {"pages": pages}
