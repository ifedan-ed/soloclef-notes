from enum import StrEnum

from pydantic import BaseModel, field_validator


class DocumentParser(StrEnum):
    """Which parser an upload asked for.

    LOCAL is pymupdf4llm on this box; CLOUD is whatever
    ``ETL_CLOUD_SERVICE`` names. See ``app.etl_pipeline.capabilities``.
    """

    LOCAL = "local"
    CLOUD = "cloud"


class EtlRequest(BaseModel):
    file_path: str
    filename: str
    estimated_pages: int = 0
    #: None means nobody chose: the server default parser handles the file,
    #: or the other parser when only that one can (connector syncs).
    parser: DocumentParser | None = None

    @field_validator("filename")
    @classmethod
    def filename_must_not_be_empty(cls, v: str) -> str:
        if not v.strip():
            raise ValueError("filename must not be empty")
        return v


class EtlResult(BaseModel):
    markdown_content: str
    etl_service: str
    actual_pages: int = 0
    content_type: str
    #: The parser's markdown for each source page (page ``i + 1`` at index
    #: ``i``) when it reports page breaks; used only to stamp chunks with the
    #: page they came from. Not cached: a cache hit falls back to the PDF's
    #: text layer.
    page_texts: list[str] | None = None
