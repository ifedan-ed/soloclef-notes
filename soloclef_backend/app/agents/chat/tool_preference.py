"""A per-message request from the composer's Tools menu to use one platform.

The retrieval scope decides which specialists exist for a turn; this is a
narrower hint on top of it, carried in the turn's context block (not the
cached system prompt) so it applies to exactly one message.
"""

from __future__ import annotations

from enum import StrEnum

from app.agents.chat.retrieval_scope import RetrievalScope


class ToolPreference(StrEnum):
    WEB = "web"
    GOOGLE_SEARCH = "google_search"
    YOUTUBE = "youtube"
    REDDIT = "reddit"
    INSTAGRAM = "instagram"
    TIKTOK = "tiktok"
    GOOGLE_MAPS = "google_maps"
    INDEED = "indeed"
    AMAZON = "amazon"
    WALMART = "walmart"
    # Run on the workspace's own keys; offered only where the key exists.
    EXA = "exa"
    TAVILY = "tavily"
    BROWSERBASE = "browserbase"


# Specialist (subagent) each preference routes to; see WEB_RETRIEVAL_SUBAGENTS.
_SPECIALIST: dict[ToolPreference, str] = {
    ToolPreference.WEB: "web_crawler",
    ToolPreference.GOOGLE_SEARCH: "google_search",
    ToolPreference.YOUTUBE: "youtube",
    ToolPreference.REDDIT: "reddit",
    ToolPreference.INSTAGRAM: "instagram",
    ToolPreference.TIKTOK: "tiktok",
    ToolPreference.GOOGLE_MAPS: "google_maps",
    ToolPreference.INDEED: "indeed",
    ToolPreference.AMAZON: "amazon",
    ToolPreference.WALMART: "walmart",
    ToolPreference.EXA: "exa_search",
    ToolPreference.TAVILY: "tavily_search",
    ToolPreference.BROWSERBASE: "browserbase",
}

_LABEL: dict[ToolPreference, str] = {
    ToolPreference.WEB: "Web",
    ToolPreference.GOOGLE_SEARCH: "Google Search",
    ToolPreference.YOUTUBE: "YouTube",
    ToolPreference.REDDIT: "Reddit",
    ToolPreference.INSTAGRAM: "Instagram",
    ToolPreference.TIKTOK: "TikTok",
    ToolPreference.GOOGLE_MAPS: "Google Maps",
    ToolPreference.INDEED: "Indeed",
    ToolPreference.AMAZON: "Amazon",
    ToolPreference.WALMART: "Walmart",
    ToolPreference.EXA: "Exa",
    ToolPreference.TAVILY: "Tavily",
    ToolPreference.BROWSERBASE: "Browserbase",
}


# What a picked tool can and cannot do, where its name alone would mislead.
# The "Web" tool was described to the agent as "web search" while it only
# reads pages at URLs, so the agent guessed URLs (thread 117: all three dead).
_NOTE: dict[ToolPreference, str] = {
    ToolPreference.WEB: (
        "The web_crawler specialist reads pages at URLs you give it; it cannot "
        "search the web. Give it the specific pages most likely to answer. If "
        "the request needs a search to find pages, say in your answer that the "
        "Web tool reads pages and the Google Search tool searches."
    ),
}


def specialist_for(preference: ToolPreference) -> str:
    return _SPECIALIST[preference]


def scope_for_tool_preference(
    scope: RetrievalScope, preference: ToolPreference | None
) -> RetrievalScope:
    """A forced platform needs the web specialists, so a docs-only turn widens."""
    if preference is None or scope.allows_web:
        return scope
    return RetrievalScope.ALL


def render_tool_preference(preference: ToolPreference | None) -> str | None:
    if preference is None:
        return None
    specialist = _SPECIALIST[preference]
    label = _LABEL[preference]
    note = f" {_NOTE[preference]}" if preference in _NOTE else ""
    return (
        "<tool_preference>\n"
        f"For this message the user chose the {label} tool. Use the "
        f"{specialist} specialist (delegate to it with the task tool) to "
        "gather what the request needs before answering, even if another "
        "source could also work. If that specialist is unavailable or finds "
        f"nothing, say so, then answer as best you can.{note}\n"
        "</tool_preference>"
    )
