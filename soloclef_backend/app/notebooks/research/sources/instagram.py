"""Instagram: the posts, reels and profiles Google finds for the words.

A one-word query that names an account gives that account's latest posts
instead (``POSTS_PER_PROFILE`` each). See the scraper's README.
"""

from __future__ import annotations

from app.capabilities.instagram.scrape.definition import INSTAGRAM_SCRAPE
from app.capabilities.instagram.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from ..snippets import compact_count, join_meta
from .plan import SourcePlan
from .rows import keep, make_row

POSTS_PER_PROFILE = 3


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(
        search_queries=[query],
        max_items=limit,
        max_per_target=POSTS_PER_PROFILE,
    )


def _title(item) -> str | None:
    caption = (item.caption or "").strip().splitlines()
    if caption and caption[0].strip():
        return caption[0]
    if item.ownerUsername:
        return f"Post by @{item.ownerUsername}"
    return None


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        if item.error:
            continue
        likes = compact_count(item.likesCount)
        rows.append(
            make_row(
                source,
                url=item.url,
                title=_title(item),
                snippet=item.caption or item.alt,
                thumbnail_url=item.displayUrl,
                meta=join_meta(
                    f"@{item.ownerUsername}" if item.ownerUsername else None,
                    f"{likes} likes" if likes else None,
                ),
            )
        )
    return keep(rows)


PLAN = SourcePlan(capability=INSTAGRAM_SCRAPE, build_input=build_input, to_rows=to_rows)
