"""Open a URL in a remote (CDP) browser and take its rendered HTML.

Browserbase hands back a CDP ``connectUrl``; Playwright attaches to it, opens
the page, waits for the DOM (``domcontentloaded``), then gives scripts a
little longer to settle (``networkidle``, best effort). A page whose main load
never finishes is still read if it painted something: JavaScript-heavy sites
often keep a request open forever, which is the "waitForMainLoadState timed
out after 15000ms" the live test hit.

The browser runs in Browserbase's cloud, not on this server, so it cannot
reach this network; still, nothing from a non-public address is kept: every
main-frame navigation (the first load, redirects, script-driven moves) is
noted and checked with ``is_publicly_routable``, and the final URL too, and a
page that went through a private or unresolvable host is refused rather than
added as a source. Requests are observed, not intercepted: pausing each one
over the remote CDP link would slow every page down.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from dataclasses import dataclass
from typing import Protocol

from app.utils.crawl import is_publicly_routable

logger = logging.getLogger(__name__)

CONNECT_TIMEOUT_MS = 15_000
NAVIGATION_TIMEOUT_MS = 25_000
SETTLE_TIMEOUT_MS = 6_000
MAX_HTML_CHARS = 5_000_000


class PageLoadError(Exception):
    """Nothing usable came back from the page."""


class PageRefusedError(PageLoadError):
    """The page (or a redirect) pointed at a non-public address."""


@dataclass(frozen=True)
class LoadedPage:
    url: str
    title: str | None
    html: str
    # True when the main load timed out and what had painted was read.
    partial: bool = False


class PageLoader(Protocol):
    async def __call__(self, connect_url: str, url: str) -> LoadedPage: ...


async def _is_public(url: str) -> bool:
    return await asyncio.to_thread(is_publicly_routable, url)


async def load_over_cdp(connect_url: str, url: str) -> LoadedPage:
    """Read ``url`` in the browser at ``connect_url``; the caller ends the session."""
    from playwright.async_api import (
        Error as PlaywrightError,
        TimeoutError as PlaywrightTimeout,
        async_playwright,
    )

    navigations: list[str] = []

    def note(frame) -> None:
        if frame.parent_frame is None:
            navigations.append(frame.url)

    async with async_playwright() as playwright:
        browser = await playwright.chromium.connect_over_cdp(
            connect_url, timeout=CONNECT_TIMEOUT_MS
        )
        try:
            context = (
                browser.contexts[0] if browser.contexts else await browser.new_context()
            )
            page = context.pages[0] if context.pages else await context.new_page()
            page.on("framenavigated", note)
            partial = False
            try:
                await page.goto(
                    url, wait_until="domcontentloaded", timeout=NAVIGATION_TIMEOUT_MS
                )
            except PlaywrightTimeout:
                partial = True
                logger.info("page %s did not finish loading; reading what painted", url)
            except PlaywrightError as exc:
                raise PageLoadError(str(exc).splitlines()[0][:200]) from exc
            if not partial:
                with contextlib.suppress(PlaywrightTimeout):
                    await page.wait_for_load_state(
                        "networkidle", timeout=SETTLE_TIMEOUT_MS
                    )
            final_url = page.url
            visited = {u for u in [*navigations, final_url] if u.startswith("http")}
            for visited_url in visited:
                if not await _is_public(visited_url):
                    raise PageRefusedError("the page led to a non-public address")
            html = await page.content()
            title = (await page.title()) or None
        finally:
            try:
                await browser.close()
            except Exception:
                logger.debug("closing the CDP browser failed", exc_info=True)
    if not html or not html.strip():
        raise PageLoadError("the page was empty")
    return LoadedPage(
        url=final_url, title=title, html=html[:MAX_HTML_CHARS], partial=partial
    )
