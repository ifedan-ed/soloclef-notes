"""Open one page in Browserbase with the workspace's key and keep its text.

The browser session, the public-address checks and the extraction are the
research box's (``app/notebooks/research/page_read``): a URL that is not a
public address is refused before any session starts, and a page that
redirected to one is refused after. Unlike "Read a web page" there is no
crawler fallback: the person picked Browserbase, so a failure is reported,
not quietly replaced.

Every failure comes back as a sentence (``CapabilityUnavailableError``), never
as an exception the chat turn would die on: besides the known ones, anything
else the remote browser throws (a CDP attach that times out, a page whose
JavaScript navigates away while it is read) is logged and reported as
``COULD_NOT_READ``, as the research box's reader does.
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable

from app.capabilities.core import Executor
from app.capabilities.core.progress import emit_progress
from app.capabilities.core.unavailable import CapabilityUnavailableError
from app.notebooks.research.page_read.browser import (
    PageLoader,
    PageLoadError,
    PageRefusedError,
)
from app.notebooks.research.page_read.browserbase_api import (
    BrowserbaseApi,
    BrowserbaseError,
)
from app.notebooks.research.page_read.reader import read_with_browserbase
from app.notebooks.research.web_apis.keys import browserbase_key
from app.utils.crawl import is_publicly_routable

from .schemas import PageText, ReadPageInput, ReadPageOutput

logger = logging.getLogger(__name__)

KeyLookup = Callable[[], Awaitable[str | None]]
ApiFactory = Callable[[str], BrowserbaseApi]

NOT_CONNECTED = (
    "Browserbase isn't connected in this workspace, so the page wasn't opened."
)
NOT_PUBLIC = "That address isn't a public web page, so it wasn't opened."
DID_NOT_LOAD = "The page didn't load in Browserbase in time."
NOTHING_READABLE = "Browserbase opened the page but found no readable text on it."
COULD_NOT_READ = "Browserbase couldn't read the page."


def _workspace_key_lookup(workspace_id: int) -> KeyLookup:
    async def lookup() -> str | None:
        from app.db import async_session_maker

        async with async_session_maker() as session:
            return await browserbase_key(session, workspace_id)

    return lookup


def build_read_page_executor(
    workspace_id: int,
    *,
    lookup_key: KeyLookup | None = None,
    api_factory: ApiFactory = BrowserbaseApi,
    loader: PageLoader | None = None,
) -> Executor:
    lookup = lookup_key or _workspace_key_lookup(workspace_id)

    async def execute(payload: ReadPageInput) -> ReadPageOutput:
        if not await asyncio.to_thread(is_publicly_routable, payload.url):
            raise CapabilityUnavailableError(NOT_PUBLIC)
        key = await lookup()
        if not key:
            raise CapabilityUnavailableError(NOT_CONNECTED)
        emit_progress("starting", "Opening the page in Browserbase")
        try:
            content = await read_with_browserbase(
                payload.url, api_factory(key), loader=loader
            )
        except PageRefusedError:
            raise CapabilityUnavailableError(NOT_PUBLIC) from None
        except BrowserbaseError as exc:
            raise CapabilityUnavailableError(str(exc)) from None
        except (PageLoadError, TimeoutError):
            raise CapabilityUnavailableError(DID_NOT_LOAD) from None
        except Exception:
            # Playwright's own errors (its TimeoutError is not the builtin
            # one) and anything else unexpected: the session is already
            # released by ``browserbase_session``; the turn goes on.
            logger.warning("Browserbase read of %s failed", payload.url, exc_info=True)
            raise CapabilityUnavailableError(COULD_NOT_READ) from None
        if content is None:
            raise CapabilityUnavailableError(NOTHING_READABLE)
        emit_progress("done", "Read the page")
        markdown = content.markdown
        final_url = content.metadata.get("final_url")
        return ReadPageOutput(
            items=[
                PageText(
                    url=payload.url,
                    final_url=final_url,
                    title=content.title,
                    markdown=markdown[: payload.max_chars],
                    truncated=len(markdown) > payload.max_chars,
                    partial_load=content.metadata.get("partial_load") == "true",
                )
            ]
        )

    return execute
