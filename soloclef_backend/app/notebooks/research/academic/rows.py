"""A result row for a paper: the byline (authors, venue, year) as its meta line."""

from __future__ import annotations

from ..schemas import ResearchSource, ResultRow
from ..snippets import join_meta
from ..sources.rows import make_row
from .paper import PaperRef

BYLINE_AUTHORS = 3


def byline(paper: PaperRef) -> str | None:
    authors = paper.authors[:BYLINE_AUTHORS]
    names = ", ".join(authors)
    if names and len(paper.authors) > BYLINE_AUTHORS:
        names += " et al."
    cited = f"Cited by {paper.cited_by}" if paper.cited_by else None
    return join_meta(
        names or None, paper.venue, str(paper.year) if paper.year else None, cited
    )


def paper_row(
    source: ResearchSource,
    *,
    url: str | None,
    title: str | None,
    snippet: str | None,
    paper: PaperRef,
) -> ResultRow | None:
    row = make_row(source, url=url, title=title, snippet=snippet, meta=byline(paper))
    return row.model_copy(update={"paper": paper}) if row else None
