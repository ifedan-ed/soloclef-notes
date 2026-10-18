"""Search Exa or Tavily with the workspace's key: one billed call per search.

A provider this workspace has no key for is reported as unavailable (the menu
greys it out; a direct API call gets an inline error), never attempted.
Nothing is retried: each attempt spends the user's own API credit.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable

import httpx
from sqlalchemy.ext.asyncio import AsyncSession

from ..schemas import ResultRow
from ..source_errors import SourceUnavailableError
from . import exa, tavily
from .keys import PROVIDERS, Provider, workspace_keys
from .requests import ApiReply, ApiRequest, ApiSender

logger = logging.getLogger(__name__)

TIMEOUT_S = 30.0
NAMES: dict[Provider, str] = {"exa": "Exa", "tavily": "Tavily"}
_BUILDERS = {"exa": exa.build_request, "tavily": tavily.build_request}
_PARSERS = {"exa": exa.to_rows, "tavily": tavily.to_rows}
# 402: Exa out of credit; 429: rate limit; 432/433: Tavily plan/pay-as-you-go limit.
_QUOTA_STATUSES = frozenset({402, 429, 432, 433})

KeyLookup = Callable[[], Awaitable[dict[Provider, str]]]


def is_web_api(source: str) -> bool:
    return source in PROVIDERS


async def send_json(request: ApiRequest) -> ApiReply:
    """POST once, straight to the provider (API calls do not go through the scraping proxy)."""
    async with httpx.AsyncClient(timeout=TIMEOUT_S) as client:
        response = await client.post(
            request.url, headers=request.headers, json=request.body
        )
    try:
        data = response.json()
    except ValueError:
        data = None
    return ApiReply(status=response.status_code, data=data)


def not_connected(provider: Provider) -> SourceUnavailableError:
    return SourceUnavailableError(
        f"{NAMES[provider]} isn't connected in this workspace. "
        "Add it under Settings -> Connectors to search with it.",
        code="source_not_connected",
    )


def _error_for(provider: Provider, status: int) -> Exception:
    name = NAMES[provider]
    if status in (401, 403):
        return SourceUnavailableError(
            f"{name} refused this workspace's API key. Check the {name} connector.",
            code="source_key_rejected",
        )
    if status in _QUOTA_STATUSES:
        return SourceUnavailableError(
            f"{name} turned the search down: the key is out of credit or over its rate limit.",
            code="source_quota",
        )
    if status >= 500:
        return SourceUnavailableError(
            f"{name} didn't answer just now. Try again in a minute.",
            code="source_busy",
        )
    return RuntimeError(f"{name} search answered HTTP {status}")


class WebApiSearch:
    """Exa and Tavily for one workspace; keys are read once, when first needed."""

    def __init__(self, lookup_keys: KeyLookup, send: ApiSender = send_json) -> None:
        self._lookup_keys = lookup_keys
        self._send = send
        self._keys: dict[Provider, str] | None = None

    @classmethod
    def for_workspace(
        cls, session: AsyncSession, workspace_id: int, send: ApiSender = send_json
    ) -> WebApiSearch:
        return cls(lambda: workspace_keys(session, workspace_id), send)

    async def _key_map(self) -> dict[Provider, str]:
        if self._keys is None:
            self._keys = await self._lookup_keys()
        return self._keys

    async def availability(self) -> dict[Provider, bool]:
        keys = await self._key_map()
        return {provider: provider in keys for provider in PROVIDERS}

    async def search(
        self, provider: Provider, query: str, limit: int, *, deep: bool
    ) -> list[ResultRow]:
        key = (await self._key_map()).get(provider)
        if not key:
            raise not_connected(provider)
        request = _BUILDERS[provider](query, limit, deep=deep, key=key)
        try:
            reply = await self._send(request)
        except httpx.HTTPError as exc:
            logger.warning(
                "%s search could not be reached: %s", provider, type(exc).__name__
            )
            raise SourceUnavailableError(
                f"{NAMES[provider]} didn't answer just now. Try again in a minute.",
                code="source_busy",
            ) from None
        if reply.status != 200:
            raise _error_for(provider, reply.status)
        return _PARSERS[provider](reply.data)[:limit]
