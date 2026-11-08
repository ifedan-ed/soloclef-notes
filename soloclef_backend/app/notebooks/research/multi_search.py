"""Search several sources at once and merge what they find into one list.

Each source runs side by side with its own time limit and its own bill (its own
``CapabilityRunner``). A source that fails or runs out of time is reported and
the others still answer; only when every source fails does the search fail.
Results are interleaved (the first of each source, then the second of each, and
so on) so no source buries the others, and a page two sources both found is
listed once, under the source picked first. Fast and deep apply to each source
exactly as they do to a search of one.
"""

from __future__ import annotations

import asyncio
import itertools
import logging
from collections.abc import AsyncIterator, Callable, Mapping
from dataclasses import dataclass

from app.services.etl_credit_service import InsufficientCreditsError

from .academic.clients import DEFAULT_CLIENTS, AcademicClients
from .capability_runner import CapabilityRunner
from .dedupe import dedupe
from .enrichment import PageReader, enrich
from .schemas import ResearchSource, ResultRow, SearchRequest
from .search import (
    DEEP_LIMIT,
    FAST_LIMIT,
    KEEPALIVE_S,
    OUT_OF_CREDIT,
    SEARCH_FAILED,
    find,
    phase,
)
from .source_errors import SourceUnavailableError
from .thumbnails.links import with_proxied_images
from .web_apis.search import WebApiSearch, is_web_api
from .web_search import SearxngPage, searxng_page

logger = logging.getLogger(__name__)

# Long enough for Scholar's first captcha solve; a source slower than this is
# dropped so the others are not held back.
SOURCE_TIMEOUT_S = 90.0
SOURCE_TIMEOUT = "This source took too long to answer."
_SEARCHING_FROM = 5
_SEARCHING_TO = 35


@dataclass(frozen=True)
class SourceOutcome:
    """What one source answered: its rows, or why it could not."""

    source: ResearchSource
    rows: list[ResultRow] | None = None
    code: str | None = None
    message: str | None = None

    def event(self) -> dict:
        if self.rows is not None:
            return {
                "type": "provider",
                "source": self.source,
                "status": "done",
                "count": len(self.rows),
            }
        return {
            "type": "provider",
            "source": self.source,
            "status": "error",
            "code": self.code,
            "message": self.message,
        }


def interleave(groups: list[list[ResultRow]]) -> list[ResultRow]:
    """Round-robin across the sources' lists, then drop pages already listed."""
    merged = [
        row
        for tier in itertools.zip_longest(*groups)
        for row in tier
        if row is not None
    ]
    return dedupe(merged)


async def _search_one(
    request: SearchRequest,
    source: ResearchSource,
    runner: CapabilityRunner,
    searxng_fetch: SearxngPage,
    academic: AcademicClients,
    web_apis: WebApiSearch | None,
) -> SourceOutcome:
    limit = DEEP_LIMIT if request.mode == "deep" else FAST_LIMIT
    try:
        rows = await asyncio.wait_for(
            find(
                request.for_source(source),
                runner,
                limit,
                searxng_fetch,
                academic,
                web_apis,
            ),
            SOURCE_TIMEOUT_S,
        )
    except TimeoutError:
        return SourceOutcome(source, code="source_timeout", message=SOURCE_TIMEOUT)
    except InsufficientCreditsError:
        return SourceOutcome(source, code="out_of_credit", message=OUT_OF_CREDIT)
    except SourceUnavailableError as unavailable:
        return SourceOutcome(source, code=unavailable.code, message=unavailable.message)
    except Exception:
        logger.exception("notebook research search failed (%s)", source)
        return SourceOutcome(source, code="search_failed", message=SEARCH_FAILED)
    return SourceOutcome(source, rows=rows)


def _reader_by_row_source(
    rows: list[ResultRow], reader_for: Callable[[ResearchSource], PageReader]
) -> PageReader:
    """Read each page on the bill of the source that found it."""
    source_of = {row.url: row.source for row in rows}

    async def read(url: str):
        return await reader_for(source_of[url])(url)

    return read


async def multi_research_events(
    request: SearchRequest,
    runners: Mapping[ResearchSource, CapabilityRunner],
    reader_for: Callable[[ResearchSource], PageReader],
    searxng_fetch: SearxngPage = searxng_page,
    academic: AcademicClients = DEFAULT_CLIENTS,
    web_apis: WebApiSearch | None = None,
) -> AsyncIterator[dict]:
    """Phase and per-source events, then one merged results event and done.

    Every source first reports ``searching``, then ``done`` with its count or
    ``error`` with a code. Only when all of them fail does the search end with
    an error event, worded after the first source picked.
    """
    sources = request.selected
    yield phase("searching", _SEARCHING_FROM)
    for source in sources:
        yield {"type": "provider", "source": source, "status": "searching"}

    tasks: dict[asyncio.Future, ResearchSource] = {}
    outcomes: dict[ResearchSource, SourceOutcome] = {}
    try:
        if web_apis is not None and any(is_web_api(s) for s in sources):
            # Read the workspace's keys once, before the sources share the session.
            await web_apis.availability()
        tasks = {
            asyncio.ensure_future(
                _search_one(
                    request,
                    source,
                    runners[source],
                    searxng_fetch,
                    academic,
                    web_apis,
                )
            ): source
            for source in sources
        }
        pending = set(tasks)
        progress = _SEARCHING_FROM
        while pending:
            finished, pending = await asyncio.wait(
                pending, timeout=KEEPALIVE_S, return_when=asyncio.FIRST_COMPLETED
            )
            for task in finished:
                outcome = task.result()
                outcomes[outcome.source] = outcome
                yield outcome.event()
            share = _SEARCHING_FROM + int(
                (_SEARCHING_TO - _SEARCHING_FROM) * len(outcomes) / len(sources)
            )
            progress = max(share, min(_SEARCHING_TO, progress + (0 if finished else 3)))
            yield phase("searching", progress)
    except Exception:
        logger.exception("notebook research search failed (%s)", sources)
        yield {"type": "error", "code": "search_failed", "message": SEARCH_FAILED}
        return
    finally:
        # The reader went away mid-search: stop every source with it.
        for task in tasks:
            if not task.done():
                task.cancel()

    answered = [outcomes[s] for s in sources if outcomes[s].rows is not None]
    if not answered:
        first = outcomes[sources[0]]
        yield {
            "type": "error",
            "code": first.code,
            "message": first.message,
            "source": first.source,
        }
        return

    rows = interleave([outcome.rows or [] for outcome in answered])
    if request.mode == "deep" and rows:
        yield phase("reading", 40)
        read = _reader_by_row_source(rows, reader_for)
        async for done, total, enriched in enrich(rows, request.query, read):
            rows = enriched
            yield phase("reading", 40 + int(45 * done / total))

    yield phase("ranking", 95)
    rows = [with_proxied_images(row) for row in rows]
    yield {"type": "results", "results": [row.model_dump() for row in rows]}
    yield {"type": "done"}
