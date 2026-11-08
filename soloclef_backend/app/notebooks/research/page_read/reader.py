"""Read a pasted URL for a notebook: Browserbase first, the crawler as fallback.

Browserbase renders the page in a real cloud browser, so JavaScript-heavy
pages come back with their content. The session is always ended
(``browserbase_session``). When Browserbase is not connected, refuses, times
out or returns nothing, the page goes through the ordinary crawler instead
(``fetch_content``), which works for most pages; its redirects are walked
first (``public_http``) so it never connects to a private address. A page that
led to a non-public address is refused outright, never retried through the
crawler.

Credits: a successful Browserbase read is charged as one crawled page, the
same meter as a research import (``WEB_CRAWL``); the crawler fallback charges
itself through the runner as an import does.
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable
from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

from app.utils.crawl import is_publicly_routable

from ..capability_runner import CapabilityRunner
from ..ingest.fetch import fetch_content
from ..ingest.fetched import FetchedContent
from ..web_apis.keys import browserbase_key
from .browser import (
    LoadedPage,
    PageLoader,
    PageLoadError,
    PageRefusedError,
    load_over_cdp,
)
from .browserbase_api import BrowserbaseApi, BrowserbaseError, browserbase_session
from .extract import extract_page
from .public_http import NotPublicError, final_public_url

logger = logging.getLogger(__name__)

# Everything Browserbase gets for one page, session start to last byte.
BROWSERBASE_BUDGET_S = 60.0

KeyLookup = Callable[[AsyncSession, int], Awaitable[str | None]]
ApiFactory = Callable[[str], BrowserbaseApi]
Crawl = Callable[
    [AsyncSession, CapabilityRunner, str, UUID | str], Awaitable[FetchedContent | None]
]
RedirectWalk = Callable[[str], Awaitable[str]]


class PageNotPublicError(Exception):
    """The URL (or where it led) is not a public address."""


async def read_with_browserbase(
    url: str,
    api: BrowserbaseApi,
    *,
    loader: PageLoader | None = None,
    budget_s: float = BROWSERBASE_BUDGET_S,
) -> FetchedContent | None:
    """The page as a source, or None when it rendered nothing readable."""
    loader = loader or load_over_cdp
    async with browserbase_session(api) as session:
        page: LoadedPage = await asyncio.wait_for(
            loader(session.connect_url, url), timeout=budget_s
        )
    extracted = extract_page(page.html)
    if not extracted.markdown:
        return None
    metadata = {**extracted.metadata, "reader": "browserbase"}
    if page.url and page.url != url:
        metadata["final_url"] = page.url
    if page.partial:
        metadata["partial_load"] = "true"
    title = extracted.title or page.title
    if title:
        metadata["page_title"] = title
    return FetchedContent(markdown=extracted.markdown, title=title, metadata=metadata)


async def read_web_page(
    session: AsyncSession,
    runner: CapabilityRunner,
    url: str,
    user_id: UUID | str,
    *,
    key_lookup: KeyLookup | None = None,
    api_factory: ApiFactory | None = None,
    loader: PageLoader | None = None,
    crawl: Crawl | None = None,
    walk_redirects: RedirectWalk | None = None,
) -> FetchedContent | None:
    """A ``ContentFetcher`` for ``fill_document``."""
    # Looked up here, not bound as defaults, so a stand-in can be patched in.
    key_lookup = key_lookup or browserbase_key
    api_factory = api_factory or BrowserbaseApi
    loader = loader or load_over_cdp
    crawl = crawl or fetch_content
    walk_redirects = walk_redirects or final_public_url

    if not await asyncio.to_thread(is_publicly_routable, url):
        raise PageNotPublicError(url)
    key = await key_lookup(session, runner.workspace_id)
    if key:
        try:
            content = await read_with_browserbase(url, api_factory(key), loader=loader)
        except PageRefusedError as exc:
            raise PageNotPublicError(url) from exc
        except (BrowserbaseError, PageLoadError, TimeoutError) as exc:
            logger.info("Browserbase could not read %s (%s); crawling it", url, exc)
            content = None
        except Exception:
            logger.warning(
                "Browserbase read of %s failed; crawling it", url, exc_info=True
            )
            content = None
        if content is not None:
            await runner.charge_pages(1)
            return content
    # The crawler runs on this server: walk the redirects first, each hop
    # public and connected to the address checked, and crawl where it lands.
    try:
        target = await walk_redirects(url)
    except NotPublicError as exc:
        raise PageNotPublicError(url) from exc
    content = await crawl(session, runner, target, user_id)
    if content is not None:
        content = FetchedContent(
            markdown=content.markdown,
            title=content.title,
            metadata={**content.metadata, "reader": "crawler"},
        )
    return content
