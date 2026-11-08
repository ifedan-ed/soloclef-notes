"""TikTok: the query's words run as hashtag feeds.

TikTok's web search is login-gated: an anonymous visitor (even in a real
browser on a residential IP) gets the search page with a "Log in" prompt where
the results go, and no search XHR ever fires — so it is a product wall, not a
bot wall that a proxy or captcha solve gets past. Hashtag feeds still load
anonymously, so the query becomes up to two hashtags: the whole query run
together when it is short, and its longest distinctive words. Results are
videos under those tags, which is looser than a search; the dropdown says
TikTok may return nothing for that reason.
"""

from __future__ import annotations

import re

from app.capabilities.tiktok.scrape.definition import TIKTOK_SCRAPE
from app.capabilities.tiktok.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from ..snippets import compact_count, join_meta
from .plan import SourcePlan
from .rows import keep, make_row

MAX_TAGS = 2
_WORD = re.compile(r"[^\W_]+", re.UNICODE)
_STOPWORDS = frozenset(
    {
        "and",
        "the",
        "for",
        "with",
        "from",
        "that",
        "this",
        "what",
        "how",
        "why",
        "when",
        "are",
        "was",
        "you",
        "your",
        "about",
        "into",
        "best",
    }
)


def hashtags_for(query: str) -> list[str]:
    """Up to two tags: the short query joined, then its longest distinctive words."""
    words = [w.lower() for w in _WORD.findall(query)]
    tags: list[str] = []
    if 1 < len(words) <= 2:
        tags.append("".join(words))
    distinctive = sorted(
        (w for w in dict.fromkeys(words) if len(w) > 2 and w not in _STOPWORDS),
        key=len,
        reverse=True,
    )
    for word in distinctive:
        if word not in tags:
            tags.append(word)
    return tags[:MAX_TAGS] or ["".join(words)[:100] or "tiktok"]


def build_input(query: str, limit: int) -> ScrapeInput:
    tags = hashtags_for(query)
    return ScrapeInput(
        hashtags=tags,
        max_items=limit,
        results_per_page=-(-limit // len(tags)),
    )


def _duration(seconds: int | None) -> str | None:
    if not seconds:
        return None
    return f"{seconds // 60}:{seconds % 60:02d}"


def _title(text: str | None, author: str | None) -> str | None:
    lines = [line for line in (text or "").splitlines() if line.strip()]
    if lines:
        return lines[0]
    return f"Video by @{author}" if author else None


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        if getattr(item, "errorCode", None):
            continue
        author = item.authorMeta.name or item.authorMeta.nickName
        plays = compact_count(item.playCount)
        rows.append(
            make_row(
                source,
                url=item.webVideoUrl,
                title=_title(item.text, author),
                snippet=item.text,
                thumbnail_url=item.videoMeta.coverUrl,
                meta=join_meta(
                    f"@{author}" if author else None,
                    _duration(item.videoMeta.duration),
                    f"{plays} views" if plays else None,
                ),
            )
        )
    return keep(rows)


PLAN = SourcePlan(capability=TIKTOK_SCRAPE, build_input=build_input, to_rows=to_rows)
