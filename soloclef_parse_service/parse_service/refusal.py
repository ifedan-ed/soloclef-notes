"""Why a file was not turned into Markdown, as a value the caller can branch on.

A refusal is a result, not a crash: the service worked and the answer is no.
The caller (the Go ingest tier) maps each kind to its own typed error, so the
kinds are a wire contract. Add one here and there together.
"""

from __future__ import annotations

from typing import Final

#: The file parsed and no page held any text: a scan. OCR is off on purpose
#: (it cost about a minute a page on CPU and dropped the spaces between
#: words), so the file is refused instead of being indexed empty.
NO_TEXT_LAYER: Final = "no_text_layer"
#: The extension is not one this parser reads well. Decided before the file
#: is opened, from the name alone.
UNSUPPORTED_FORMAT: Final = "unsupported_format"
#: The parser raised, died, or ran past its time limit on this file. Sending
#: the same bytes again will end the same way.
PARSE_ERROR: Final = "parse_error"


def refusal(kind: str, message: str) -> dict:
    """The refusal as the worker process hands it back (it crosses a pipe)."""
    return {"refusal": {"kind": kind, "message": message}}
