"""Indeed job search."""

from __future__ import annotations

from app.capabilities.indeed.scrape.definition import INDEED_SCRAPE
from app.capabilities.indeed.scrape.schemas import ScrapeInput, ScrapeOutput

from ..schemas import ResearchSource, ResultRow
from ..snippets import join_meta
from .plan import SourcePlan
from .rows import keep, make_row


def build_input(query: str, limit: int) -> ScrapeInput:
    return ScrapeInput(
        search_queries=[query], max_items=limit, max_items_per_query=limit
    )


def to_rows(output: ScrapeOutput, source: ResearchSource) -> list[ResultRow]:
    rows = []
    for item in output.items:
        rows.append(
            make_row(
                source,
                url=item.jobUrl or item.applyUrl,
                title=item.title,
                snippet=item.descriptionText or item.descriptionHtml,
                meta=join_meta(
                    item.company, item.formattedLocation, item.salary.salaryText
                ),
            )
        )
    return keep(rows)


PLAN = SourcePlan(capability=INDEED_SCRAPE, build_input=build_input, to_rows=to_rows)
