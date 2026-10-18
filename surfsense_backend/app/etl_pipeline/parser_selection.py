"""Turn what a caller asked for into the parser that will read the file.

Two jobs: accept the per-upload choice from new and old clients alike
(``parser=local|cloud``, or the retired ``processing_mode=basic|premium``),
and pick the parser for a document when nobody chose one.
"""

from __future__ import annotations

import logging
from pathlib import PurePosixPath
from typing import Final

from app.config import config
from app.etl_pipeline.capabilities import ParserCapability, parser_capability
from app.etl_pipeline.etl_document import DocumentParser
from app.etl_pipeline.exceptions import (
    EtlServiceUnavailableError,
    EtlUnsupportedFileError,
)

logger = logging.getLogger(__name__)

#: The retired Basic/Premium toggle, as sent by older web builds, the
#: desktop folder sync and the MCP server.
LEGACY_PROCESSING_MODES: Final[dict[str, DocumentParser]] = {
    "basic": DocumentParser.LOCAL,
    "premium": DocumentParser.CLOUD,
}


def parse_parser_choice(value: str | None) -> DocumentParser | None:
    """Read ``local``/``cloud`` or a legacy ``basic``/``premium``; None if neither."""
    if not value:
        return None
    normalized = value.strip().lower()
    try:
        return DocumentParser(normalized)
    except ValueError:
        return LEGACY_PROCESSING_MODES.get(normalized)


def default_parser() -> DocumentParser:
    """The server's default choice (``ETL_DEFAULT_PARSER``, checked at startup).

    LOCAL if the value is somehow unset or bad by the time it is read.
    """
    return parse_parser_choice(config.ETL_DEFAULT_PARSER) or DocumentParser.LOCAL


class ParserUnavailableError(ValueError):
    """The caller explicitly chose a parser this deployment cannot run."""

    def __init__(self, capability: ParserCapability) -> None:
        self.capability = capability
        super().__init__(
            f"The {capability.parser.value} parser is unavailable "
            f"({capability.unavailable_reason})"
        )


def resolve_upload_parser(
    parser: str | None, processing_mode: str | None = None
) -> DocumentParser:
    """The parser an upload request asked for.

    ``parser`` wins over the legacy ``processing_mode``; with neither, the
    server default applies. An explicit ``parser=cloud`` that cannot run is
    refused, but a legacy ``premium`` degrades to the default: old clients
    sent it without knowing what the server could do.
    """
    explicit = parse_parser_choice(parser)
    if explicit is not None:
        capability = parser_capability(explicit)
        if not capability.available:
            raise ParserUnavailableError(capability)
        return explicit

    legacy = parse_parser_choice(processing_mode)
    if legacy is not None:
        if parser_capability(legacy).available:
            return legacy
        logger.info(
            "Legacy processing_mode=%s maps to the unavailable %s parser; "
            "using the default parser instead",
            processing_mode,
            legacy.value,
        )
    return default_parser()


def _other(parser: DocumentParser) -> DocumentParser:
    return (
        DocumentParser.CLOUD if parser is DocumentParser.LOCAL else DocumentParser.LOCAL
    )


def route_document(filename: str, parser: DocumentParser | None) -> ParserCapability:
    """The parser that reads ``filename``, or an error saying why none can.

    A chosen parser is honoured strictly. With no choice, the default parser
    reads the file if it can, else the other one if that can.
    """
    ext = PurePosixPath(filename).suffix.lower()
    if parser is not None:
        return _require(parser_capability(parser), ext)

    preferred = parser_capability(default_parser())
    if preferred.available and ext in preferred.document_extensions:
        return preferred
    fallback = parser_capability(_other(preferred.parser))
    if fallback.available and ext in fallback.document_extensions:
        return fallback
    return _require(preferred, ext)


def automatically_routable_extensions() -> frozenset[str]:
    """Document extensions some available parser opens when nobody chooses."""
    extensions: frozenset[str] = frozenset()
    for choice in DocumentParser:
        capability = parser_capability(choice)
        if capability.available:
            extensions |= capability.document_extensions
    return extensions


def _require(capability: ParserCapability, ext: str) -> ParserCapability:
    if not capability.available:
        raise EtlServiceUnavailableError(
            f"The {capability.parser.value} parser "
            f"({capability.service or 'unconfigured'}) is unavailable: "
            f"{capability.unavailable_reason}"
        )
    if ext not in capability.document_extensions:
        raise EtlUnsupportedFileError(
            f"File type {ext or '(none)'} is not supported by the "
            f"{capability.parser.value} parser ({capability.service})"
        )
    return capability


def files_the_parser_cannot_read(
    filenames: list[str], parser: DocumentParser, *, vision_requested: bool
) -> list[str]:
    """Upload names that ``parser`` would reject, checked before anything is stored.

    Parser-agnostic categories always pass. Images pass when the upload asked
    for the vision LLM, which reads them regardless of the parser.
    """
    from app.etl_pipeline.file_classifier import FileCategory, classify_file

    accepted = parser_capability(parser).document_extensions
    rejected: list[str] = []
    for filename in filenames:
        category = classify_file(filename)
        if category == FileCategory.IMAGE and vision_requested:
            continue
        if category == FileCategory.UNSUPPORTED or (
            category in (FileCategory.DOCUMENT, FileCategory.IMAGE)
            and PurePosixPath(filename).suffix.lower() not in accepted
        ):
            rejected.append(filename)
    return rejected


def upload_parser_from_task(value: str | None) -> DocumentParser:
    """The parser a queued upload task carries; the default when it carries none.

    Tasks queued before the Local/Cloud split carry ``basic``/``premium``.
    """
    return parse_parser_choice(value) or default_parser()
