"""Turn scraped text of any shape into a short plain-text snippet."""

from __future__ import annotations

import html
import re

from .schemas import MAX_SNIPPET_CHARS

_TAG = re.compile(r"<[^>]+>")
_MD_IMAGE = re.compile(r"!\[[^\]]*\]\([^)]*\)")
_MD_LINK = re.compile(r"\[([^\]]*)\]\([^)]*\)")
_MD_MARKS = re.compile(r"(^|\s)#{1,6}\s+|[*_`>|]+")
_SPACE = re.compile(r"\s+")


def plain_text(value: str | None) -> str:
    """Strip HTML and markdown decoration and collapse whitespace."""
    if not value:
        return ""
    text = html.unescape(_TAG.sub(" ", value))
    text = _MD_IMAGE.sub(" ", text)
    text = _MD_LINK.sub(r"\1", text)
    text = _MD_MARKS.sub(" ", text)
    return _SPACE.sub(" ", text).strip()


def trim(value: str | None, limit: int = MAX_SNIPPET_CHARS) -> str:
    """Plain text cut at a word boundary to fit ``limit`` characters."""
    text = plain_text(value)
    if len(text) <= limit:
        return text
    cut = text[: limit - 1]
    space = cut.rfind(" ")
    if space > limit * 0.6:
        cut = cut[:space]
    return cut.rstrip(" ,.;:-") + "…"


def excerpt(markdown: str | None, query: str, limit: int = MAX_SNIPPET_CHARS) -> str:
    """The passage of a page that best matches the query, as a snippet.

    Paragraphs are scored by how many query words they mention; ties go to the
    earlier paragraph, so a page with no match yields its opening text.
    """
    words = {w for w in re.findall(r"\w+", query.lower()) if len(w) > 2}
    best, best_score = "", -1
    for block in re.split(r"\n\s*\n", markdown or ""):
        text = plain_text(block)
        if len(text) < 40:
            continue
        lowered = text.lower()
        score = sum(1 for word in words if word in lowered)
        if score > best_score:
            best, best_score = text, score
    return trim(best, limit)


def compact_count(value: int | float | None) -> str | None:
    """1234 -> '1.2K', 1_200_000 -> '1.2M'."""
    if value is None:
        return None
    number = float(value)
    for bound, suffix in ((1e9, "B"), (1e6, "M"), (1e3, "K")):
        if abs(number) >= bound:
            return f"{number / bound:.1f}".rstrip("0").rstrip(".") + suffix
    return str(int(number))


def join_meta(*parts: str | None) -> str | None:
    """Non-empty parts joined with a middle dot, or None when all are empty."""
    kept = [part.strip() for part in parts if part and part.strip()]
    return " · ".join(kept) if kept else None
