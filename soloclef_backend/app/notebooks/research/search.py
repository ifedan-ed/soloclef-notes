"""Run one research search and narrate it as progress events.

Fast research returns the first page of results from one search. Deep research
asks for more results and then reads the top pages, so their snippets quote
the page instead of the search engine's teaser. Academic sources (PubMed,
arXiv, preprints, Scholar) answer through their own APIs; see ``academic``.
Exa and Tavily answer with the workspace's own API keys; see ``web_apis``.
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import AsyncIterator

from app.exceptions import ForbiddenError
from app.services.etl_credit_service import InsufficientCreditsError

from .academic.clients import DEFAULT_CLIENTS, AcademicClients
from .academic.search import is_academic, search_academic
from .capability_runner import CapabilityRunner
from .dedupe import dedupe
from .enrichment import PageReader, enrich
from .schemas import ResultRow, SearchRequest
from .source_errors import SourceUnavailableError
from .sources.registry import plan_for
from .thumbnails.links import with_proxied_images
from .web_apis.search import WebApiSearch, is_web_api
from .web_search import SearxngPage, search_web, searxng_page

logger = logging.getLogger(__name__)

FAST_LIMIT = 10
DEEP_LIMIT = 25
# A slow search (Scholar's first captcha solve can take minutes) still sends a
# searching event this often, so proxies never close the quiet stream and the
# bar keeps moving.
KEEPALIVE_S = 15.0
_SEARCHING_CEILING = 35

OUT_OF_CREDIT = "You're out of credit for this search. Top up and try again."
SEARCH_FAILED = "The search didn't go through. Try again, or pick another source."
# A platform scraper raises ForbiddenError when the site refused every attempt
# (Reddit's wall, Instagram's login wall, ...); the panel words "source_blocked"
# itself, this English text is for API callers.
SOURCE_BLOCKED = "{name} is blocking automated searches right now."
_PLATFORM_NAMES = {
    "google_search": "Google",
    "youtube": "YouTube",
    "reddit": "Reddit",
    "instagram": "Instagram",
    "tiktok": "TikTok",
    "google_maps": "Google Maps",
    "indeed": "Indeed",
    "amazon": "Amazon",
    "walmart": "Walmart",
}


def phase(name: str, progress: int) -> dict:
    return {"type": "phase", "phase": name, "progress": progress}


async def find(
    request: SearchRequest,
    runner: CapabilityRunner,
    limit: int,
    searxng_fetch: SearxngPage = searxng_page,
    academic: AcademicClients = DEFAULT_CLIENTS,
    web_apis: WebApiSearch | None = None,
) -> list[ResultRow]:
    if is_web_api(request.source):
        if web_apis is None:
            raise RuntimeError("Exa/Tavily search needs the workspace's keys")
        rows = await web_apis.search(
            request.source, request.query, limit, deep=request.mode == "deep"
        )
        return dedupe(rows)[:limit]
    if is_academic(request.source):
        rows = await search_academic(
            request.source,
            request.query,
            deep=request.mode == "deep",
            clients=academic,
        )
        return dedupe(rows)
    if request.source == "web":
        rows = await search_web(request.query, limit, runner, searxng_fetch)
    else:
        plan = plan_for(request.source)
        try:
            output = await runner.run(
                plan.capability, plan.build_input(request.query, limit)
            )
        except ForbiddenError as refused:
            logger.info("research source %s refused: %s", request.source, refused)
            raise SourceUnavailableError(
                SOURCE_BLOCKED.format(
                    name=_PLATFORM_NAMES.get(request.source, request.source)
                ),
                code="source_blocked",
            ) from refused
        rows = plan.to_rows(output, request.source)
    return dedupe(rows)[:limit]


async def research_events(
    request: SearchRequest,
    runner: CapabilityRunner,
    read_page: PageReader,
    searxng_fetch: SearxngPage = searxng_page,
    academic: AcademicClients = DEFAULT_CLIENTS,
    web_apis: WebApiSearch | None = None,
) -> AsyncIterator[dict]:
    """Phase events, then one results event and done; or an error event and stop."""
    deep = request.mode == "deep"
    yield phase("searching", 5)
    searching = asyncio.ensure_future(
        find(
            request,
            runner,
            DEEP_LIMIT if deep else FAST_LIMIT,
            searxng_fetch,
            academic,
            web_apis,
        )
    )
    try:
        progress = 5
        while not searching.done():
            await asyncio.wait({searching}, timeout=KEEPALIVE_S)
            if not searching.done():
                progress = min(_SEARCHING_CEILING, progress + 3)
                yield phase("searching", progress)
        rows = searching.result()
    except InsufficientCreditsError:
        yield {"type": "error", "code": "out_of_credit", "message": OUT_OF_CREDIT}
        return
    except SourceUnavailableError as unavailable:
        yield {
            "type": "error",
            "code": unavailable.code,
            "message": unavailable.message,
        }
        return
    except Exception:
        logger.exception("notebook research search failed (%s)", request.source)
        yield {"type": "error", "code": "search_failed", "message": SEARCH_FAILED}
        return
    finally:
        # The reader went away mid-search: stop the search with it.
        if not searching.done():
            searching.cancel()

    if deep and rows:
        yield phase("reading", 40)
        async for done, total, enriched in enrich(rows, request.query, read_page):
            rows = enriched
            yield phase("reading", 40 + int(45 * done / total))

    yield phase("ranking", 95)
    rows = [with_proxied_images(row) for row in rows]
    yield {"type": "results", "results": [row.model_dump() for row in rows]}
    yield {"type": "done"}
