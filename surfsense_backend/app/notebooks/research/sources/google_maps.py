"""Google Maps place search."""

from __future__ import annotations

from app.capabilities.google_maps.scrape.definition import GOOGLE_MAPS_SCRAPE
from app.capabilities.google_maps.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from ..snippets import compact_count, join_meta
from .plan import SourcePlan
from .rows import keep, make_row


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(search_queries=[query], max_places=limit)


def _rating(item) -> str | None:
    if item.totalScore is None:
        return None
    reviews = compact_count(item.reviewsCount)
    return f"{item.totalScore:g}★" + (f" ({reviews})" if reviews else "")


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        rows.append(
            make_row(
                source,
                url=item.url or item.website,
                title=item.title,
                snippet=join_meta(item.address, item.description) or "",
                thumbnail_url=item.imageUrl,
                meta=join_meta(item.categoryName, _rating(item), item.price),
            )
        )
    return keep(rows)


PLAN = SourcePlan(
    capability=GOOGLE_MAPS_SCRAPE, build_input=build_input, to_rows=to_rows
)
