"""Reddit post search (comments are skipped: the post is the source)."""

from __future__ import annotations

from app.capabilities.reddit.scrape.definition import REDDIT_SCRAPE
from app.capabilities.reddit.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from ..snippets import compact_count, join_meta
from .plan import SourcePlan
from .rows import keep, make_row


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(
        search_queries=[query],
        sort="relevance",
        skip_comments=True,
        max_items=limit,
        max_posts=limit,
        max_comments=0,
    )


def _community(item) -> str | None:
    if item.parsedCommunityName:
        return f"r/{item.parsedCommunityName}"
    return item.communityName


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        if item.dataType not in (None, "post"):
            continue
        upvotes = compact_count(item.upVotes)
        comments = compact_count(item.numberOfComments)
        rows.append(
            make_row(
                source,
                url=item.url,
                title=item.title,
                snippet=item.body,
                thumbnail_url=item.thumbnailUrl,
                meta=join_meta(
                    _community(item),
                    f"{upvotes} upvotes" if upvotes else None,
                    f"{comments} comments" if comments else None,
                ),
            )
        )
    return keep(rows)


PLAN = SourcePlan(capability=REDDIT_SCRAPE, build_input=build_input, to_rows=to_rows)
