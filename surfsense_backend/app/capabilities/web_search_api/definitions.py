"""``exa.web_search`` and ``tavily.web_search``, built for one workspace.

Billed per search on ``exa_search`` / ``tavily_search`` (``billing_action_prices``,
seeded by ``c0022_chat_web_search_prices``) to the workspace's owner, like
every other paid web tool; a search that could not run is not charged.
"""

from __future__ import annotations

from app.capabilities.core import ActivityDescriptor, BillingUnit, Capability
from app.notebooks.research.web_apis.keys import Provider
from app.notebooks.research.web_apis.search import NAMES

from .executor import build_web_search_executor
from .schemas import WebSearchInput, WebSearchOutput

_UNITS: dict[Provider, BillingUnit] = {
    "exa": BillingUnit.EXA_SEARCH,
    "tavily": BillingUnit.TAVILY_SEARCH,
}


def web_search_capability(provider: Provider, workspace_id: int) -> Capability:
    name = NAMES[provider]
    return Capability(
        name=f"{provider}.web_search",
        description=(
            f"Search the web with {name}. Returns up to 10 results: title, URL, "
            "a short matching passage, and the site and date when known."
        ),
        input_schema=WebSearchInput,
        output_schema=WebSearchOutput,
        executor=build_web_search_executor(provider, workspace_id),
        billing_unit=_UNITS[provider],
        activity=ActivityDescriptor(
            active_title=f"Searching the web with {name}",
            completed_title=f"Searched the web with {name}",
            category="research",
            icon_key="search",
            integration_key=provider,
        ),
    )
