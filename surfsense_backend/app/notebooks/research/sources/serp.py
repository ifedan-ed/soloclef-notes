"""Google Search (and SearXNG, which answers in the same SERP shape)."""

from __future__ import annotations

from urllib.parse import urlsplit

from app.capabilities.google_search.scrape.definition import GOOGLE_SEARCH_SCRAPE
from app.capabilities.google_search.scrape.schemas import ScrapeInput, ScrapeOutput
from app.proprietary.platforms.google_search import SerpItem

from ..schemas import ResearchSource, ResultRow
from ..snippets import join_meta
from .plan import SourcePlan
from .rows import keep, make_row

RESULTS_PER_PAGE = 10


def pages_for(limit: int) -> int:
    return max(1, min(3, -(-limit // RESULTS_PER_PAGE)))


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(queries=[query], max_pages_per_query=pages_for(limit))


def serp_rows(items: list[SerpItem], source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in items:
        for result in item.organicResults:
            host = (urlsplit(result.url or "").hostname or "").removeprefix("www.")
            rows.append(
                make_row(
                    source,
                    url=result.url,
                    title=result.title,
                    snippet=result.description,
                    meta=join_meta(host, result.date),
                )
            )
    return keep(rows)


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    return serp_rows(output.items, source)


PLAN = SourcePlan(
    capability=GOOGLE_SEARCH_SCRAPE, build_input=build_input, to_rows=to_rows
)
