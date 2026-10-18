"""Walmart product search (listing cards only, no detail pages or reviews)."""

from __future__ import annotations

from app.capabilities.walmart.scrape.definition import WALMART_SCRAPE
from app.capabilities.walmart.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from .plan import SourcePlan
from .products import product_meta
from .rows import keep, make_row


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(
        search_terms=[query],
        max_items=limit,
        include_details=False,
        include_reviews_sample=False,
    )


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        if item.model_extra and item.model_extra.get("error"):
            continue
        rows.append(
            make_row(
                source,
                url=item.url,
                title=item.name,
                snippet=item.shortDescription or item.category or item.brand,
                thumbnail_url=item.thumbnailImage,
                meta=product_meta(item.price, item.stars),
            )
        )
    return keep(rows)


PLAN = SourcePlan(capability=WALMART_SCRAPE, build_input=build_input, to_rows=to_rows)
