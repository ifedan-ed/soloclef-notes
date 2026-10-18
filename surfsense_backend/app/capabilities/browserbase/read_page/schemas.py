"""``browserbase.read_page`` I/O: a public URL in, the page's text out."""

from __future__ import annotations

from pydantic import BaseModel, Field

from app.capabilities.core.validation import HttpUrlStr

DEFAULT_MAX_CHARS = 30_000
MAX_CHARS = 100_000
"""Ceiling on the page text kept per read; a longer page is cut, and says so."""


class ReadPageInput(BaseModel):
    url: HttpUrlStr = Field(description="The public http(s) page to open.")
    max_chars: int = Field(
        default=DEFAULT_MAX_CHARS,
        ge=1_000,
        le=MAX_CHARS,
        description="How much of the page's text to keep.",
    )

    @property
    def estimated_units(self) -> int:
        return 1


class PageText(BaseModel):
    url: str
    final_url: str | None = Field(
        default=None, description="Where the page ended up, when it redirected."
    )
    title: str | None = None
    markdown: str
    truncated: bool = False
    partial_load: bool = Field(
        default=False,
        description="The page never finished loading; this is what had painted.",
    )


class ReadPageOutput(BaseModel):
    items: list[PageText] = Field(default_factory=list)

    @property
    def billable_units(self) -> int:
        """A page that came back with text counts as one read page."""
        return len(self.items)

    @property
    def captcha_attempts(self) -> int:
        return 0
