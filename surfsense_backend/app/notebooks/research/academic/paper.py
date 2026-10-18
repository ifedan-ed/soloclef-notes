"""The scholarly identity and facts a research result carries for a paper.

Rows from PubMed, arXiv, the preprint servers and Scholar all describe papers
the same way, so the UI can show badges and open-access marks and the import can
find a paper again by DOI, PMID or arXiv id whichever source it came from. The
same model comes back on import, from the client, so every field is bounded.
"""

from __future__ import annotations

import re
from typing import Literal

from pydantic import BaseModel, Field, field_validator

from ..urls import is_http_url

Badge = Literal["pubmed", "pmc", "arxiv", "medrxiv", "biorxiv", "scholar"]
PreprintServer = Literal["medrxiv", "biorxiv"]

_DOI = re.compile(r"^10\.\d{4,9}/\S{1,190}$")
_PMID = re.compile(r"^\d{1,9}$")
_PMCID = re.compile(r"^PMC\d{1,9}$")
_ARXIV = re.compile(r"^(\d{4}\.\d{4,5}|[a-z\-]+(\.[A-Z]{2})?/\d{7})$")

MAX_AUTHORS = 30


class PaperRef(BaseModel):
    doi: str | None = None
    pmid: str | None = None
    pmcid: str | None = None
    arxiv_id: str | None = None
    server: PreprintServer | None = None
    version: str | None = Field(default=None, max_length=8)
    pdf_url: str | None = Field(default=None, max_length=2048)
    authors: list[str] = Field(default_factory=list, max_length=MAX_AUTHORS)
    venue: str | None = Field(default=None, max_length=300)
    year: int | None = Field(default=None, ge=1000, le=3000)
    published: str | None = Field(default=None, max_length=32)
    categories: list[str] = Field(default_factory=list, max_length=12)
    cited_by: int | None = Field(default=None, ge=0)
    badges: list[Badge] = Field(default_factory=list, max_length=6)
    open_access: bool = False

    @field_validator("doi", mode="before")
    @classmethod
    def _doi(cls, value: object) -> object:
        if value is None or value == "":
            return None
        text = str(value).strip().lower()
        text = re.sub(r"^(https?://(dx\.)?doi\.org/|doi:)", "", text)
        if not _DOI.match(text):
            raise ValueError("not a DOI")
        return text

    @field_validator("pmid", mode="before")
    @classmethod
    def _pmid(cls, value: object) -> object:
        if value is None or value == "":
            return None
        text = str(value).strip()
        if not _PMID.match(text):
            raise ValueError("not a PMID")
        return text

    @field_validator("pmcid", mode="before")
    @classmethod
    def _pmcid(cls, value: object) -> object:
        if value is None or value == "":
            return None
        text = str(value).strip().upper()
        if text.isdigit():
            text = f"PMC{text}"
        if not _PMCID.match(text):
            raise ValueError("not a PMC id")
        return text

    @field_validator("arxiv_id", mode="before")
    @classmethod
    def _arxiv(cls, value: object) -> object:
        if value is None or value == "":
            return None
        text = re.sub(r"v\d+$", "", str(value).strip())
        if not _ARXIV.match(text):
            raise ValueError("not an arXiv id")
        return text

    @field_validator("pdf_url")
    @classmethod
    def _pdf_url(cls, value: str | None) -> str | None:
        if value is None:
            return None
        return value.strip() if is_http_url(value) else None

    @field_validator("authors", "categories", mode="before")
    @classmethod
    def _short_strings(cls, value: object) -> object:
        if not isinstance(value, list):
            return value
        return [str(item).strip()[:120] for item in value if str(item).strip()][
            :MAX_AUTHORS
        ]

    @field_validator("badges", mode="before")
    @classmethod
    def _unique_badges(cls, value: object) -> object:
        if not isinstance(value, list):
            return value
        return list(dict.fromkeys(value))


def with_badge(paper: PaperRef, badge: Badge) -> PaperRef:
    if badge in paper.badges:
        return paper
    return paper.model_copy(update={"badges": [*paper.badges, badge]})
