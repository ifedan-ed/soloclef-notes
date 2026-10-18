"""Run one Exa or Tavily search with the workspace's own key.

The request, the key lookup and the error wording are research's
(``app/notebooks/research/web_apis``), so a search from the chat and one from
the research box behave the same. Exa runs its ``auto`` search and Tavily a
``basic`` one: the kinds each bills at its lowest per-search price. Nothing
is retried: every attempt spends the key's own credit.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable

from app.capabilities.core import Executor
from app.capabilities.core.progress import emit_progress
from app.capabilities.core.unavailable import CapabilityUnavailableError
from app.notebooks.research.source_errors import SourceUnavailableError
from app.notebooks.research.web_apis.keys import Provider, workspace_keys
from app.notebooks.research.web_apis.requests import ApiSender
from app.notebooks.research.web_apis.search import NAMES, WebApiSearch, send_json

from .schemas import WebSearchInput, WebSearchOutput, WebSearchResult

KeyLookup = Callable[[], Awaitable[dict[Provider, str]]]

_DEEP: dict[Provider, bool] = {"exa": True, "tavily": False}


def _workspace_key_lookup(workspace_id: int) -> KeyLookup:
    async def lookup() -> dict[Provider, str]:
        from app.db import async_session_maker

        async with async_session_maker() as session:
            return await workspace_keys(session, workspace_id)

    return lookup


def build_web_search_executor(
    provider: Provider,
    workspace_id: int,
    *,
    lookup_keys: KeyLookup | None = None,
    send: ApiSender = send_json,
) -> Executor:
    lookup = lookup_keys or _workspace_key_lookup(workspace_id)
    name = NAMES[provider]

    async def execute(payload: WebSearchInput) -> WebSearchOutput:
        emit_progress("starting", f"Searching {name}")
        try:
            rows = await WebApiSearch(lookup, send).search(
                provider, payload.query, payload.max_results, deep=_DEEP[provider]
            )
        except SourceUnavailableError as exc:
            raise CapabilityUnavailableError(exc.message) from None
        except RuntimeError as exc:
            # An answer the provider gave but we could not use (e.g. HTTP 400).
            raise CapabilityUnavailableError(
                f"{name} couldn't run this search ({exc})."
            ) from None
        emit_progress("done", f"{name} found {len(rows)} result(s)")
        return WebSearchOutput(
            provider=provider,
            query=payload.query,
            items=[
                WebSearchResult(
                    title=row.title, url=row.url, snippet=row.snippet, meta=row.meta
                )
                for row in rows
            ],
        )

    return execute
