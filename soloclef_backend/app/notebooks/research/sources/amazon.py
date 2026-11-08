"""Amazon product search (listing cards only, no detail-page fan-out)."""

from __future__ import annotations

from app.capabilities.amazon.scrape.definition import AMAZON_SCRAPE
from app.capabilities.amazon.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from .plan import SourcePlan
from .products import product_meta
from .rows import keep, make_row


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(search_terms=[query], max_items=limit, include_details=False)


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        if item.model_extra and item.model_extra.get("error"):
            continue
        rows.append(
            make_row(
                source,
                url=item.url,
                title=item.title,
                snippet=item.description
                or "; ".join(item.features[:3])
                or item.breadCrumbs
                or item.brand,
                thumbnail_url=item.thumbnailImage,
                meta=product_meta(item.price, item.stars),
            )
        )
    return keep(rows)


PLAN = SourcePlan(capability=AMAZON_SCRAPE, build_input=build_input, to_rows=to_rows)
