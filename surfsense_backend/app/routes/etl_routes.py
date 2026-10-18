"""``GET /api/v1/etl/capabilities``: the upload dialog's parser choice.

Each upload picks Local or Cloud. Whether Cloud can run here, what each
choice costs and which files each opens are all deployment facts, so the
backend reports them from :mod:`app.etl_pipeline.capabilities` -- the same
table the pipeline and the page-credit billing read -- instead of the
frontend keeping a copy that would go stale.

Read-only, and requires nothing but a session: the payload is the
operator's own parser configuration, which the upload UI has to know anyway.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends
from pydantic import BaseModel

from app.auth.context import AuthContext
from app.config import config
from app.etl_pipeline.capabilities import ParserCapability, parser_capability
from app.etl_pipeline.etl_document import DocumentParser
from app.etl_pipeline.parser_selection import default_parser
from app.users import require_session_context
from app.utils.file_extensions import IMAGE_EXTENSIONS

router = APIRouter()


class ParserCapabilityRead(BaseModel):
    parser: DocumentParser
    #: The ETL service behind the choice, e.g. "PYMUPDF4LLM", "LLAMACLOUD".
    service: str | None
    available: bool
    #: "missing_api_key" | "not_installed" | "unknown_service" | null.
    unavailable_reason: str | None
    #: Page-credit multiplier. 0 = free.
    page_multiplier: int
    #: Document extensions this parser opens, sorted, with the leading dot.
    document_extensions: list[str]


class AlwaysHandledExtensionsRead(BaseModel):
    """File types that do not depend on the parser choice."""

    plaintext: list[str]
    direct_convert: list[str]
    audio: list[str]
    #: Read by the workspace vision LLM when the upload asks for it;
    #: otherwise by the chosen parser, if that parser opens the format.
    image: list[str]


class EtlCapabilitiesRead(BaseModel):
    default_parser: DocumentParser
    parsers: list[ParserCapabilityRead]
    always_handled: AlwaysHandledExtensionsRead
    page_credit_billing_enabled: bool


def _parser_read(capability: ParserCapability) -> ParserCapabilityRead:
    return ParserCapabilityRead(
        parser=capability.parser,
        service=capability.service,
        available=capability.available,
        unavailable_reason=capability.unavailable_reason,
        page_multiplier=capability.page_multiplier,
        document_extensions=sorted(capability.document_extensions),
    )


@router.get("/etl/capabilities", response_model=EtlCapabilitiesRead)
async def get_etl_capabilities(
    _auth: AuthContext = Depends(require_session_context),
) -> EtlCapabilitiesRead:
    from app.etl_pipeline.file_classifier import (
        AUDIO_EXTENSIONS,
        DIRECT_CONVERT_EXTENSIONS,
        PLAINTEXT_EXTENSIONS,
    )

    return EtlCapabilitiesRead(
        default_parser=default_parser(),
        parsers=[_parser_read(parser_capability(choice)) for choice in DocumentParser],
        always_handled=AlwaysHandledExtensionsRead(
            plaintext=sorted(PLAINTEXT_EXTENSIONS),
            direct_convert=sorted(DIRECT_CONVERT_EXTENSIONS),
            audio=sorted(AUDIO_EXTENSIONS),
            image=sorted(IMAGE_EXTENSIONS),
        ),
        page_credit_billing_enabled=bool(config.ETL_CREDIT_BILLING_ENABLED),
    )
