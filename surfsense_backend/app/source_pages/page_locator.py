"""Find the page, or page range, each chunk's text came from.

Chunks are cut from markdown; pages are whatever text a source has for each
page (the parser's per-page markdown, or the PDF's text layer). The two never
match character for character -- markdown adds ``#``, ``|`` and ``**``, a text
layer hyphenates and reorders table cells -- so matching is on runs of words:
every three consecutive words of a chunk ("shingles") vote for the pages that
contain them.

A shingle found on many pages (a running header, a copyright line) says
nothing about where a chunk is, so it does not vote. A chunk whose shingles
mostly appear nowhere (a vision-model picture description, a heading too short
to have shingles) gets no page of its own, but inherits one when the chunks on
either side agree on it.
"""

from __future__ import annotations

import re
from collections import Counter, defaultdict
from collections.abc import Sequence
from dataclasses import dataclass

#: Words per shingle. Three is long enough that a shingle rarely recurs by
#: chance across pages, short enough that a table cell or a hyphenated line
#: break only costs the shingles that straddle it.
SHINGLE_WORDS = 3
#: A shingle on more pages than this is boilerplate and does not vote. In a
#: short document a shingle on every page is boilerplate too.
MAX_PAGES_PER_SHINGLE = 3
#: Share of a chunk's voting shingles that must be found at all before any
#: page is trusted.
MIN_MATCHED_SHARE = 0.3
#: Share of the matched shingles a page next to the best one needs to be
#: counted as part of the chunk's range (a chunk running over a page break).
RANGE_PAGE_SHARE = 0.15

_WORD = re.compile(r"[^\W_]+")
# A word broken over a line end ("hyphen-\nated") in a text layer.
_LINE_END_HYPHEN = re.compile(r"(?<=\w)-[ \t]*\n[ \t]*(?=\w)")


@dataclass(frozen=True, slots=True)
class PageSpan:
    """1-based inclusive pages a chunk was read from."""

    start: int
    end: int


def words(text: str) -> list[str]:
    """Lower-case words of ``text``, markdown punctuation and hyphenation dropped."""
    return [w.lower() for w in _WORD.findall(_LINE_END_HYPHEN.sub("", text))]


def _shingles(tokens: list[str]) -> set[str]:
    if len(tokens) < SHINGLE_WORDS:
        return set()
    return {
        " ".join(tokens[i : i + SHINGLE_WORDS])
        for i in range(len(tokens) - SHINGLE_WORDS + 1)
    }


class PageIndex:
    """Which pages each shingle occurs on."""

    def __init__(self, page_texts: Sequence[str]) -> None:
        self.page_count = len(page_texts)
        pages_of: dict[str, set[int]] = defaultdict(set)
        for number, text in enumerate(page_texts, start=1):
            for shingle in _shingles(words(text)):
                pages_of[shingle].add(number)
        self._pages_of = pages_of
        self._max_pages = max(1, min(MAX_PAGES_PER_SHINGLE, self.page_count - 1))

    def locate(self, chunk: str, *, near: int | None = None) -> PageSpan | None:
        """The pages ``chunk`` was read from, or None when the text is not found.

        ``near`` is the page of the previous chunk: when two pages score the
        same (text repeated in the document), the one closest to it wins.
        """
        voting = 0
        matched = 0
        votes: Counter[int] = Counter()
        for shingle in _shingles(words(chunk)):
            pages = self._pages_of.get(shingle)
            if pages is not None and len(pages) > self._max_pages:
                continue
            voting += 1
            if pages:
                matched += 1
                votes.update(pages)
        if not voting or not votes or matched / voting < MIN_MATCHED_SHARE:
            return None

        top = max(votes.values())
        best = min(
            (page for page, count in votes.items() if count == top),
            key=lambda page: (abs(page - near) if near else 0, page),
        )
        floor = max(1, RANGE_PAGE_SHARE * matched)
        start = best
        while votes.get(start - 1, 0) >= floor:
            start -= 1
        end = best
        while votes.get(end + 1, 0) >= floor:
            end += 1
        return PageSpan(start, end)


def locate_chunk_pages(
    chunks: Sequence[str], page_texts: Sequence[str]
) -> list[PageSpan | None]:
    """Each chunk's pages in ``page_texts`` (page ``i + 1`` at index ``i``)."""
    if not any(text.strip() for text in page_texts):
        return [None] * len(chunks)
    index = PageIndex(page_texts)
    spans: list[PageSpan | None] = []
    near: int | None = None
    for chunk in chunks:
        span = index.locate(chunk, near=near)
        if span is not None:
            near = span.end
        spans.append(span)
    return spans


def fill_between_agreeing_neighbours(
    spans: Sequence[PageSpan | None],
) -> list[PageSpan | None]:
    """Give an unplaced chunk the page its placed neighbours both end and start on.

    Chunks are in document order, so a picture description or a lone heading
    between two chunks of page 7 is on page 7. Between pages 7 and 8 it could
    be either, so it stays unknown.
    """
    filled = list(spans)
    previous: PageSpan | None = None
    gap: list[int] = []
    for i, span in enumerate(spans):
        if span is None:
            gap.append(i)
            continue
        if gap and previous is not None and previous.end == span.start:
            for j in gap:
                filled[j] = PageSpan(span.start, span.start)
        gap = []
        previous = span
    return filled


def assign_pages(
    chunks: Sequence[str], *page_sources: Sequence[str] | None
) -> list[PageSpan | None]:
    """Pages for each chunk, trying each page source in turn.

    The first source that places a chunk wins; later sources only fill the
    chunks earlier ones could not place. Then unplaced chunks between two
    chunks of the same page join that page.
    """
    spans: list[PageSpan | None] = [None] * len(chunks)
    for source in page_sources:
        if not source or all(span is not None for span in spans):
            continue
        for i, span in enumerate(locate_chunk_pages(chunks, source)):
            if spans[i] is None:
                spans[i] = span
    return fill_between_agreeing_neighbours(spans)


__all__ = [
    "PageIndex",
    "PageSpan",
    "assign_pages",
    "fill_between_agreeing_neighbours",
    "locate_chunk_pages",
    "words",
]
