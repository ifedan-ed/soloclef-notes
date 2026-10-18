"""YouTube video search."""

from __future__ import annotations

from app.capabilities.youtube.scrape.definition import YOUTUBE_SCRAPE
from app.capabilities.youtube.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from ..snippets import compact_count, join_meta
from .plan import SourcePlan
from .rows import keep, make_row


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(search_queries=[query], max_results=limit)


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        views = compact_count(item.viewCount)
        rows.append(
            make_row(
                source,
                url=item.url,
                title=item.title,
                snippet=item.text or item.channelName,
                thumbnail_url=item.thumbnailUrl,
                meta=join_meta(
                    item.duration,
                    f"{views} views" if views else None,
                    item.channelName,
                ),
            )
        )
    return keep(rows)


PLAN = SourcePlan(capability=YOUTUBE_SCRAPE, build_input=build_input, to_rows=to_rows)
