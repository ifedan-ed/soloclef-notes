"""Wire contracts for notebook web research: search requests, result rows, imports."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field, field_validator, model_validator

from .academic.paper import PaperRef
from .urls import require_http_url

ResearchSource = Literal[
    "web",
    "google_search",
    "exa",
    "tavily",
    "youtube",
    "reddit",
    "instagram",
    "tiktok",
    "google_maps",
    "indeed",
    "amazon",
    "walmart",
    "pubmed",
    "arxiv",
    "medrxiv",
    "google_scholar",
]
ResearchMode = Literal["fast", "deep"]

MAX_QUERY_CHARS = 500
MAX_SNIPPET_CHARS = 400
MAX_IMPORT_ITEMS = 50
MAX_TITLE_CHARS = 500
# One search can ask this many sources at once.
MAX_SOURCES = 4


class SearchRequest(BaseModel):
    """One search: a query, where to look (one ``source``, or several ``sources``), how hard.

    With ``sources`` given, ``source`` becomes its first entry, so a reader of
    ``source`` alone still sees where the search mainly looked.
    """

    query: str = Field(min_length=1, max_length=MAX_QUERY_CHARS)
    source: ResearchSource = "web"
    sources: list[ResearchSource] | None = Field(
        default=None, min_length=1, max_length=MAX_SOURCES
    )
    mode: ResearchMode = "fast"

    @field_validator("query", mode="before")
    @classmethod
    def _strip(cls, value: object) -> object:
        return value.strip() if isinstance(value, str) else value

    @field_validator("sources")
    @classmethod
    def _distinct(cls, value: list[ResearchSource] | None) -> list | None:
        return list(dict.fromkeys(value)) if value else value

    @model_validator(mode="after")
    def _primary_first(self) -> SearchRequest:
        if self.sources:
            self.source = self.sources[0]
        return self

    @property
    def selected(self) -> list[ResearchSource]:
        """Every source this search asks, in the order the user picked them."""
        return list(self.sources) if self.sources else [self.source]

    def for_source(self, source: ResearchSource) -> SearchRequest:
        """The same search, asked of one source only."""
        return SearchRequest(query=self.query, source=source, mode=self.mode)


class ResultRow(BaseModel):
    """One candidate source the user can tick and import."""

    id: str
    url: str
    title: str
    snippet: str
    source: ResearchSource
    # Both are links to our image proxy (see ``thumbnails``), never the remote URL.
    thumbnail_url: str | None = None
    favicon_url: str | None = None
    meta: str | None = None
    # Set for papers (PubMed, arXiv, preprints, Scholar): ids, authors, badges.
    paper: PaperRef | None = None


class ImportItem(BaseModel):
    url: str
    title: str = Field(min_length=1, max_length=MAX_TITLE_CHARS)
    snippet: str | None = Field(default=None, max_length=4000)
    source: ResearchSource = "web"
    paper: PaperRef | None = None

    @field_validator("url")
    @classmethod
    def _http_only(cls, value: str) -> str:
        return require_http_url(value)

    @field_validator("title", mode="before")
    @classmethod
    def _strip_title(cls, value: object) -> object:
        return value.strip() if isinstance(value, str) else value


class ImportRequest(BaseModel):
    items: list[ImportItem] = Field(min_length=1, max_length=MAX_IMPORT_ITEMS)


class ResearchSourcesStatus(BaseModel):
    """Sources this workspace cannot search right now, each with the reason,
    and what "Read a web page" reads with (Browserbase when it is connected)."""

    unavailable: dict[ResearchSource, Literal["not_connected"]] = Field(
        default_factory=dict
    )
    page_reader: Literal["browserbase", "crawler"] = "crawler"


class ReadPageRequest(BaseModel):
    """A URL someone pasted to add as a source."""

    url: str

    @field_validator("url")
    @classmethod
    def _http_only(cls, value: str) -> str:
        return require_http_url(value)
