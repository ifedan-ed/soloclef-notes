"""Stub ``parse_with_pymupdf4llm`` for E2E.

The real parser runs MuPDF's layout model and, for image regions,
RapidOCR. Both are bundled rather than downloaded, so unlike the Docling
fake this one is not working around ``HF_HUB_OFFLINE=1`` -- it is here
for determinism and speed. A canary assertion that fails because an OCR
pass read the glyphs slightly differently is a bad way to spend a CI
run, and each real parse costs seconds per fixture.

Every canary PDF under ``tests/e2e/fakes/fixtures/binary/`` is produced
by ``generate_canary_pdfs.py`` and embeds its canary token as plain
``(text) Tj`` PDF text operators. Extracting those operators gives us
the canary string back, which is what the Playwright assertions look
for in the resulting Document row.
"""

from __future__ import annotations

import logging
import re
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)

# Matches the `(escaped text) Tj` text-show operator emitted by
# generate_canary_pdfs.py. Inside the parens, the escape rules are:
#   \\  -> backslash
#   \(  -> literal (
#   \)  -> literal )
# The character class [^\\()] consumes any non-escape byte; \\. consumes
# an escape sequence. Sufficient for our synthetic fixtures.
_TJ_PATTERN = re.compile(rb"\(((?:[^\\()]|\\.)*)\)\s*Tj")


def _extract_text_from_synthetic_pdf(file_path: str) -> str:
    """Pull every ``(text) Tj`` payload out of a fixture PDF in order.

    Returns an empty string if the file cannot be read. We do not try to
    handle arbitrary PDFs because the fake is only ever invoked against
    fixtures we generate ourselves.
    """
    try:
        data = Path(file_path).read_bytes()
    except OSError as exc:
        logger.warning("[fake-pymupdf4llm] could not read %s: %s", file_path, exc)
        return ""

    lines: list[str] = []
    for match in _TJ_PATTERN.finditer(data):
        raw = match.group(1)
        # Order-sensitive unescape via sentinel: protect `\\` first so
        # the subsequent `\(` / `\)` passes do not corrupt it.
        text = (
            raw.replace(rb"\\", b"\x00")
            .replace(rb"\(", b"(")
            .replace(rb"\)", b")")
            .replace(b"\x00", b"\\")
        )
        try:
            lines.append(text.decode("utf-8"))
        except UnicodeDecodeError:
            lines.append(text.decode("latin-1"))
    return "\n".join(lines)


async def fake_parse_with_pymupdf4llm(
    file_path: str,
    filename: str,
    processing_mode: str = "basic",
) -> str:
    """Drop-in replacement for ``parse_with_pymupdf4llm``.

    Returns markdown, the same shape the real parser returns, so the ETL
    pipeline needs no knowledge that it has been faked.
    """
    extracted = _extract_text_from_synthetic_pdf(file_path)
    display_name = filename or Path(file_path).name

    if extracted:
        content = f"# {display_name}\n\n{extracted}\n"
    else:
        # Empty fallback so the indexing pipeline does not error out on
        # an unexpected payload. A failing canary assertion is a much
        # clearer failure mode than a hard parser exception.
        content = (
            f"# {display_name}\n\n"
            f"(empty pymupdf4llm fake — no text-show operators found)\n"
        )

    logger.info(
        "[fake-pymupdf4llm] returning %d chars for %s (mode=%s)",
        len(content),
        display_name,
        processing_mode,
    )
    return content


def install(patches: list[Any]) -> None:
    """Patch the parser function at its definition site.

    ``EtlPipelineService._extract_document`` imports it inside the
    dispatch branch, so the import happens after this patch is in place
    and picks up the replacement.

    Fails loud rather than warning: a silent passthrough means every
    canary PDF gets a real multi-second layout + OCR parse, and any
    rename of the parser binding would go unnoticed until the suite
    mysteriously slowed down.
    """
    from unittest.mock import patch as _patch

    target = "app.etl_pipeline.parsers.pymupdf4llm.parse_with_pymupdf4llm"
    try:
        p = _patch(target, fake_parse_with_pymupdf4llm)
        p.start()
        patches.append(p)
        logger.info("[fake-pymupdf4llm] patched %s", target)
    except (ModuleNotFoundError, AttributeError) as exc:
        raise RuntimeError(
            f"Could not patch pymupdf4llm binding {target!r}: {exc!s}. "
            f"Update surfsense_backend/tests/e2e/fakes/pymupdf4llm_parser.py "
            f"to point at the new binding site."
        ) from exc
