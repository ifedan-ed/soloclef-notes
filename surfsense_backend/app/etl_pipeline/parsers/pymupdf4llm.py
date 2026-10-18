"""Local document -> markdown via pymupdf4llm.

pymupdf4llm drives MuPDF's layout engine (a bundled ONNX layout/table
model, no runtime download) and hands back GitHub-flavoured markdown
with real ``|`` tables.  It is the local, free parser: nothing leaves
the box and nothing is metered.

It deliberately does **not** describe pictures.  ``write_images`` and
``embed_images`` are pinned off so the markdown stays text; the
vision-LLM pass in ``app.etl_pipeline.picture_describer`` walks the
original file afterwards and appends the descriptions.  Two things
reading the same images twice, each for what it is good at.
"""

from __future__ import annotations

import asyncio
import logging

from app.source_pages.paged_markdown import PagedMarkdown, pages_by_number

logger = logging.getLogger(__name__)


#: OCR only the image regions the layout model flags as needing it, at
#: 150 dpi, on top of the PDF's own text layer. A forced-OCR profile
#: (every region at 300 dpi) existed as "Premium" and was retired when
#: the per-upload choice became Local vs Cloud: on a 9-page, six-table
#: paper it added only figure axis ticks for 30% more wall-clock time.
#: ``use_ocr`` stays on, so a scanned page is never silently dropped.
_OCR_DPI = 150


def _to_markdown(file_path: str) -> str:
    import pymupdf4llm

    output = pymupdf4llm.to_markdown(
        file_path,
        use_ocr=True,
        force_ocr=False,
        ocr_dpi=_OCR_DPI,
        # Pictures are the picture_describer's job, not ours.  Writing
        # them out would litter the worker's filesystem; embedding them
        # would put base64 blobs into the text we are about to chunk and
        # embed.
        write_images=False,
        embed_images=False,
        show_progress=False,
        # One entry per page, so each page's markdown is known. Joined, the
        # entries are exactly the single-string output.
        page_chunks=True,
    )
    return _join_pages(output)


def _join_pages(output) -> str:
    """The document markdown, carrying each page's markdown for page stamping."""
    if isinstance(output, str):
        return output
    numbered: list[tuple[int, str]] = []
    for position, page in enumerate(output, start=1):
        metadata = page.get("metadata") or {}
        number = metadata.get("page_number") or metadata.get("page") or position
        numbered.append((int(number), page.get("text") or ""))
    markdown = "".join(text for _, text in numbered)
    return PagedMarkdown(markdown, pages_by_number(numbered))


async def parse_with_pymupdf4llm(file_path: str, filename: str) -> str:
    """Parse a document to markdown with the local pymupdf4llm pipeline."""
    # to_markdown is synchronous and CPU-bound (layout model + OCR), so
    # it runs off the event loop rather than stalling every other
    # coroutine in the worker for the length of a parse.
    markdown = await asyncio.to_thread(_to_markdown, file_path)

    logger.info("pymupdf4llm parsed %s: %d chars", filename, len(markdown))
    return markdown
