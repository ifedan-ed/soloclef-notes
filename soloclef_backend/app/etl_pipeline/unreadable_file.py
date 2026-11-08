"""Telling someone a file cannot be read here, in words they can act on.

Two callers, one vocabulary. An upload of a format no parser opens is
refused before anything is stored (:func:`unsupported_file_type_detail`
builds the 400 body); a file that reaches the pipeline anyway -- from a
connector sync, a queued task -- is refused there
(:func:`unreadable_file_message`). Either way the refusal names the file,
the formats that do work, and the other parser when that one reads it,
rather than leaving an empty document behind or a parser error to read.

The upload body's machine-readable fields (``code``, ``parser``, ``files``)
are unchanged; ``message`` and ``accepted_extensions`` are additive, and
``message`` is what the error envelope shows
(``app.app._http_exception_handler`` reads it from a dict detail).
"""

from __future__ import annotations

from app.etl_pipeline.capabilities import parser_capability
from app.etl_pipeline.etl_document import DocumentParser
from app.etl_pipeline.parser_selection import (
    automatically_routable_extensions,
    files_the_parser_cannot_read,
)

#: Shown with every refusal: these never depend on the parser choice, so a
#: user looking at the list of document formats is not left thinking a .md or
#: a .csv needs one of them.
PARSER_AGNOSTIC_FORMATS = "plain text, Markdown, CSV, HTML and audio files"

_LABELS = {DocumentParser.LOCAL: "Local", DocumentParser.CLOUD: "Cloud"}


def _other(parser: DocumentParser) -> DocumentParser:
    return (
        DocumentParser.CLOUD if parser is DocumentParser.LOCAL else DocumentParser.LOCAL
    )


def _alternative(
    filenames: list[str], parser: DocumentParser, *, vision_requested: bool
) -> str:
    """The sentence offering the other parser, or "" when it is no help."""
    other = _other(parser)
    if not parser_capability(other).available:
        return ""
    if files_the_parser_cannot_read(
        filenames, other, vision_requested=vision_requested
    ):
        return ""
    return f" The {_LABELS[other]} parser reads them; choose it and upload again."


def unreadable_file_message(filename: str) -> str:
    """Why nothing here reads ``filename``, naming what is read instead.

    For the file types no parser opens at all, which no choice of parser
    changes -- so this names what an available parser does open rather than
    a parser the caller should have picked.
    """
    readable = sorted(automatically_routable_extensions())
    return (
        f"Nothing in this deployment can read {filename}. It reads "
        f"{', '.join(readable) or 'no document formats'}, and "
        f"{PARSER_AGNOSTIC_FORMATS}."
    )


def unsupported_file_type_detail(
    filenames: list[str],
    parser: DocumentParser,
    *,
    vision_requested: bool = False,
) -> dict[str, object]:
    """The 400 body for ``filenames`` that ``parser`` cannot read."""
    accepted = sorted(parser_capability(parser).document_extensions)
    label = _LABELS[parser]
    message = (
        f"The {label} parser cannot read {', '.join(filenames)}. "
        f"It reads {', '.join(accepted) or 'no document formats here'}, "
        f"and {PARSER_AGNOSTIC_FORMATS} are read whichever parser is chosen."
        f"{_alternative(filenames, parser, vision_requested=vision_requested)}"
    )
    return {
        "code": "unsupported_file_type",
        "parser": parser.value,
        "files": filenames,
        "accepted_extensions": accepted,
        "message": message,
    }
