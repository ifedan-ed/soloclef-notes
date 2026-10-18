"""Query terms, short passages and highlight ranges for the home search.

Pure functions: no database, no models. The service feeds them chunk and note
text; the client only draws what comes back (``snippet`` plus ``highlights``
as character ranges into it), so web and mobile highlight the same way.
"""

from __future__ import annotations

import re

# Words too common to be worth a highlight (the full-text query drops them too).
_STOPWORDS = frozenset(
    {
        "a",
        "an",
        "and",
        "are",
        "as",
        "at",
        "be",
        "but",
        "by",
        "for",
        "from",
        "has",
        "have",
        "i",
        "if",
        "in",
        "into",
        "is",
        "it",
        "its",
        "of",
        "on",
        "or",
        "so",
        "than",
        "that",
        "the",
        "their",
        "then",
        "there",
        "these",
        "they",
        "this",
        "to",
        "was",
        "were",
        "what",
        "when",
        "where",
        "which",
        "who",
        "will",
        "with",
        "you",
        "your",
    }
)

# Letters and digits only: underscores and punctuation split words.
_TOKEN = re.compile(r"[^\W_]+", re.UNICODE)
_MAX_TERMS = 8

SNIPPET_CHARS = 240


def query_terms(query: str) -> list[str]:
    """The words of ``query`` worth matching: lowercased, deduped, no stopwords.

    Single characters are dropped; they match too much to mean anything.
    """
    seen: list[str] = []
    for token in _TOKEN.findall(query.lower()):
        if token in seen:
            continue
        if len(token) < 2 or token in _STOPWORDS:
            continue
        seen.append(token)
    return seen[:_MAX_TERMS]


def prefix_tsquery(terms: list[str]) -> str | None:
    """A ``to_tsquery`` string matching every term as a word prefix.

    ``asth`` finds "asthma" while the user is still typing. The terms come
    from :func:`query_terms`, so they hold only word characters and cannot
    inject tsquery operators.
    """
    if not terms:
        return None
    return " & ".join(f"{term}:*" for term in terms)


# Every pattern here runs over a whole note (up to 200,000 characters, more
# for a note saved by other paths) on the request's event loop, so each must
# take time in proportion to the text. The link and image patterns were
# ``\[([^\]]+)\]\([^)]*\)``: from every "[" with no "]" after it (or every
# "](" with no ")"), the scan ran to the end of the text and failed, so the
# time grew with the square of the length (20,000 characters of "[" took a
# second, 200,000 about a minute and a half). A label now stops at the next
# "[" and a destination at the next "]", so scans started from different
# brackets never cover the same text twice. What that changes: a label
# holding "[" keeps its text from the last "[" (the earlier one stays as
# written), and a destination holding "]" is left as written.
# custom/README.md, "Security fixes of 2026-10-11", follow-up.
_MD_IMAGE = re.compile(r"!\[([^\[\]]*)\]\([^)\]]*\)")
_MD_LINK = re.compile(r"\[([^\[\]]+)\]\([^)\]]*\)")
_MD_MARKS = re.compile(r"(\*\*|__|`{1,3}|~~|^#{1,6}\s+|^>\s?|\|)", re.MULTILINE)
# A tag stops at the next "<" for the same reason ("<<<<…" with no ">").
_HTML_TAG = re.compile(r"<[^<>]{1,200}>")
_SPACE = re.compile(r"\s+")


def plain_text(markdown: str) -> str:
    """Markdown flattened to one line of readable text."""
    text = _MD_IMAGE.sub(r"\1", markdown or "")
    text = _MD_LINK.sub(r"\1", text)
    text = _HTML_TAG.sub(" ", text)
    text = _MD_MARKS.sub(" ", text)
    return _SPACE.sub(" ", text).strip()


def _term_pattern(terms: list[str]) -> re.Pattern[str] | None:
    if not terms:
        return None
    # Longest first so "nurses" wins over "nurse" at the same position; a term
    # must start a word, so "ma" does not light up inside "asthma".
    alternatives = "|".join(re.escape(t) for t in sorted(terms, key=len, reverse=True))
    return re.compile(rf"(?<!\w)(?:{alternatives})", re.IGNORECASE)


def highlight_ranges(text: str, terms: list[str]) -> list[tuple[int, int]]:
    """``[start, end)`` ranges of ``text`` where a term starts a word."""
    pattern = _term_pattern(terms)
    if pattern is None:
        return []
    return [(m.start(), m.end()) for m in pattern.finditer(text)]


def _snap_start(text: str, start: int) -> int:
    if start <= 0:
        return 0
    space = text.find(" ", start)
    return space + 1 if 0 <= space < start + 30 else start


def _snap_end(text: str, end: int) -> int:
    if end >= len(text):
        return len(text)
    space = text.rfind(" ", end - 30, end)
    return space if space > 0 else end


def _densest_window(text: str, ranges: list[tuple[int, int]], width: int) -> int:
    """Where the ``width``-character window covering the most distinct terms,
    then the most hits, starts: at a hit, the first such window winning.

    One pass with two ends. ``ranges`` come sorted and never overlap, so the
    hits inside the window that starts at hit ``i`` are ``i`` up to the
    first one ending past it. Comparing every hit with every other took time
    in the square of their number: 5,000 hits half a second, a long note of
    one repeated word half a minute.
    """
    words = [text[a:b].lower() for a, b in ranges]
    counts: dict[str, int] = {}
    best_start, best_key = ranges[0][0], (-1, -1)
    end = 0
    for i, (anchor, _) in enumerate(ranges):
        # counts holds the words of ranges[i:end]; empty when end <= i.
        end = max(end, i)
        while end < len(ranges) and ranges[end][1] <= anchor + width:
            counts[words[end]] = counts.get(words[end], 0) + 1
            end += 1
        key = (len(counts), end - i)
        if key > best_key:
            best_start, best_key = anchor, key
        if end > i:
            left = counts[words[i]] - 1
            if left:
                counts[words[i]] = left
            else:
                del counts[words[i]]
    return best_start


def make_snippet(
    content: str, terms: list[str], *, width: int = SNIPPET_CHARS
) -> tuple[str, list[tuple[int, int]]]:
    """A short window of ``content`` around its densest run of matches.

    Returns the snippet (with ``…`` where it was cut) and the highlight ranges
    inside it. Without a literal match (a purely semantic hit) the window is
    the start of the passage.
    """
    text = plain_text(content)
    if not text:
        return "", []
    ranges = highlight_ranges(text, terms)
    start = max(0, _densest_window(text, ranges, width) - width // 4) if ranges else 0
    start = _snap_start(text, start)
    end = _snap_end(text, min(len(text), start + width))
    snippet = text[start:end].strip()
    prefix = "… " if start > 0 else ""
    suffix = " …" if end < len(text) else ""
    snippet = f"{prefix}{snippet}{suffix}"
    return snippet, highlight_ranges(snippet, terms)


def has_literal_match(content: str, terms: list[str]) -> bool:
    return bool(highlight_ranges(plain_text(content), terms))
