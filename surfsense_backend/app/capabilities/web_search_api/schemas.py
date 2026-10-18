"""``exa.web_search`` / ``tavily.web_search`` I/O: a query in, titled links out."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field

MAX_QUERY_CHARS = 400
"""Tavily refuses longer queries; Exa takes the same cap so both behave alike."""

MAX_RESULTS = 10
"""Results per search. Exa bills 1-25 results at one price and Tavily a basic
search at one credit whatever the count, so ten costs no more than one."""


class WebSearchInput(BaseModel):
    query: str = Field(
        min_length=1,
        max_length=MAX_QUERY_CHARS,
        description="What to search the web for, in plain words.",
    )
    max_results: int = Field(
        default=MAX_RESULTS,
        ge=1,
        le=MAX_RESULTS,
        description="How many results to return (at most 10).",
    )

    @property
    def estimated_units(self) -> int:
        """One search, whatever it finds."""
        return 1


class WebSearchResult(BaseModel):
    title: str
    url: str
    snippet: str = Field(
        default="", description="A short passage of the page that matches the query."
    )
    meta: str | None = Field(
        default=None, description="Site, author and date, when the provider gives them."
    )


class WebSearchOutput(BaseModel):
    provider: Literal["exa", "tavily"]
    query: str
    items: list[WebSearchResult] = Field(default_factory=list)

    @property
    def billable_units(self) -> int:
        """The provider bills a search that answered, even with no results."""
        return 1
