"""What each per-upload parser choice is, costs, and accepts.

An upload picks **Local** or **Cloud**. Local is always pymupdf4llm with
its text-layer-first profile. Cloud is the service ``ETL_CLOUD_SERVICE``
names: LlamaCloud's agentic parse (with the Azure Document Intelligence
accelerator when configured), Datalab's Marker API in balanced mode (with
LlamaCloud behind it when that has a key), or the bundled open-source
``unstructured`` library.

Which service backs a choice, whether it can run here, what it bills per
page and which extensions it opens used to be answered in four places.
Keeping them in one table means the parsers, the page-credit billing and
the upload UI cannot drift apart; ``GET /etl/capabilities`` hands it to
the frontend as-is.
"""

from __future__ import annotations

import importlib.util
from dataclasses import dataclass
from typing import Final

from app.config import config
from app.config.parser_settings import CLOUD_SERVICES as _CLOUD_SERVICE_NAMES
from app.etl_pipeline.etl_document import DocumentParser
from app.utils.file_extensions import get_document_extensions_for_service

LOCAL_SERVICE: Final = "PYMUPDF4LLM"
#: The services ETL_CLOUD_SERVICE may name; startup refuses any other.
CLOUD_SERVICES: Final = frozenset(_CLOUD_SERVICE_NAMES)

#: Page-credit multiplier per choice. Local runs on this box and costs
#: nothing; Cloud is billed at the premium rate.
_PAGE_MULTIPLIERS: Final[dict[DocumentParser, int]] = {
    DocumentParser.LOCAL: 0,
    DocumentParser.CLOUD: 10,
}

#: Stable tokens the UI translates; not sentences.
UNAVAILABLE_UNKNOWN_SERVICE: Final = "unknown_service"
UNAVAILABLE_MISSING_API_KEY: Final = "missing_api_key"
UNAVAILABLE_NOT_INSTALLED: Final = "not_installed"


@dataclass(frozen=True)
class ParserCapability:
    """One parser choice as this deployment is configured right now."""

    parser: DocumentParser
    #: The ETL service that does the work, e.g. "PYMUPDF4LLM".
    service: str | None
    available: bool
    #: Why it cannot run, as a stable token; None when available.
    unavailable_reason: str | None
    page_multiplier: int
    document_extensions: frozenset[str]


def cloud_service() -> str | None:
    """The configured cloud service name, or None if unrecognised.

    Startup already refuses an unknown ETL_CLOUD_SERVICE; this stays strict
    so a value set some other way (a test, a patched config) fails closed.
    """
    service = (config.ETL_CLOUD_SERVICE or "").strip().upper()
    return service if service in CLOUD_SERVICES else None


def _installed(module: str) -> bool:
    try:
        return importlib.util.find_spec(module) is not None
    except (ImportError, ValueError):
        return False


def _unavailable_reason(service: str | None) -> str | None:
    if service is None:
        return UNAVAILABLE_UNKNOWN_SERVICE
    if service == LOCAL_SERVICE:
        return None if _installed("pymupdf4llm") else UNAVAILABLE_NOT_INSTALLED
    if service == "LLAMACLOUD":
        return None if config.LLAMA_CLOUD_API_KEY else UNAVAILABLE_MISSING_API_KEY
    if service == "DATALAB":
        return None if config.DATALAB_API_KEY else UNAVAILABLE_MISSING_API_KEY
    if service == "UNSTRUCTURED":
        return (
            None if _installed("langchain_unstructured") else UNAVAILABLE_NOT_INSTALLED
        )
    return UNAVAILABLE_UNKNOWN_SERVICE


def parser_capability(parser: DocumentParser) -> ParserCapability:
    """Describe ``parser`` under the current configuration."""
    service = LOCAL_SERVICE if parser is DocumentParser.LOCAL else cloud_service()
    reason = _unavailable_reason(service)
    return ParserCapability(
        parser=parser,
        service=service,
        available=reason is None,
        unavailable_reason=reason,
        page_multiplier=_PAGE_MULTIPLIERS[parser],
        document_extensions=(
            get_document_extensions_for_service(service) if service else frozenset()
        ),
    )


def page_multiplier_for(parser: DocumentParser) -> int:
    """Page-credit multiplier for a parse done by ``parser``."""
    return _PAGE_MULTIPLIERS[parser]
