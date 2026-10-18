"""Per-turn retrieval policy shared by chat routes and agent construction."""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from enum import StrEnum
from typing import Any


class RetrievalScope(StrEnum):
    """Sources the agent may read while answering one user turn."""

    DOCUMENTS = "documents"
    WEB = "web"
    ALL = "all"

    @property
    def allows_documents(self) -> bool:
        return self in {RetrievalScope.DOCUMENTS, RetrievalScope.ALL}

    @property
    def allows_web(self) -> bool:
        return self in {RetrievalScope.WEB, RetrievalScope.ALL}


# These specialists exist to retrieve public web/platform data. Connected-app
# actions remain available through mcp_discovery and are filtered tool-by-tool.
# Exa, Tavily and Browserbase run on the workspace's own keys (read from its
# MCP connectors) but return public web data, so they are web specialists
# too, never connected-app reads; ``keyed_web_specialists`` drops the ones
# without a key.
WEB_RETRIEVAL_SUBAGENTS = frozenset(
    {
        "amazon",
        "browserbase",
        "exa_search",
        "google_maps",
        "google_search",
        "indeed",
        "instagram",
        "reddit",
        "tavily_search",
        "tiktok",
        "walmart",
        "web_crawler",
        "youtube",
    }
)

_KB_RETRIEVAL_TOOLS = frozenset(
    {
        "execute_code",
        "glob",
        "grep",
        "list_tree",
        "ls",
        "read_file",
        "search_knowledge_base",
    }
)
_NATIVE_CONNECTED_APP_RETRIEVAL_TOOLS = frozenset(
    {
        "get_connected_accounts",
    }
)


@dataclass(frozen=True)
class SourcesOnly:
    """A notebook chat's retrieval: the notebook's enabled sources, nothing else.

    Set on every notebook turn, whatever scope the client sent. The document
    side is limited to the notebook's sources by the turn's document scope;
    this switches the web side off: no web specialist is built and the prompt
    says answers come from the sources only.

    ``picked_specialists`` are the web specialists the user deliberately chose
    for this one message from the composer's Tools menu. Only those exist for
    the turn, and their results are web results, cited as such, never
    notebook sources.

    ``unavailable_picks`` were picked but cannot be built this turn: Exa,
    Tavily or Browserbase without a key in the workspace (the menu's status
    can be up to a minute old, and an API client can send any pick). The
    prompt says so rather than offering a ``task(...)`` that does not exist.
    """

    picked_specialists: frozenset[str] = frozenset()
    unavailable_picks: frozenset[str] = frozenset()

    @classmethod
    def picking(cls, specialists: Iterable[str]) -> SourcesOnly:
        return cls(frozenset(specialists) & WEB_RETRIEVAL_SUBAGENTS)

    def without(self, unbuildable: Iterable[str]) -> SourcesOnly:
        """These picks moved to ``unavailable_picks`` (the rest unchanged)."""
        gone = self.picked_specialists & frozenset(unbuildable)
        if not gone:
            return self
        return SourcesOnly(
            self.picked_specialists - gone, self.unavailable_picks | gone
        )

    def cache_token(self) -> str:
        """Stable identity for the compiled-agent cache key."""
        token = "sources-only:" + ",".join(sorted(self.picked_specialists))
        if self.unavailable_picks:
            token += ";unavailable:" + ",".join(sorted(self.unavailable_picks))
        return token


def excluded_retrieval_subagents(
    scope: RetrievalScope, sources_only: SourcesOnly | None = None
) -> set[str]:
    """Whole specialists to omit because every operation retrieves web data."""

    if sources_only is not None:
        return set(WEB_RETRIEVAL_SUBAGENTS - sources_only.picked_specialists)
    return set() if scope.allows_web else set(WEB_RETRIEVAL_SUBAGENTS)


def denied_retrieval_tools(
    scope: RetrievalScope,
    *,
    subagent_name: str,
    tools: list[Any],
) -> set[str]:
    """Return tool names that a mixed-capability agent may not read with."""

    denied: set[str] = set()
    if not scope.allows_documents and subagent_name.startswith("knowledge_base"):
        denied.update(_KB_RETRIEVAL_TOOLS)

    if not scope.allows_web and subagent_name == "mcp_discovery":
        denied.update(_NATIVE_CONNECTED_APP_RETRIEVAL_TOOLS)
        for tool in tools:
            metadata = getattr(tool, "metadata", None) or {}
            # MCP read-only annotations are normalized to hitl=False by the
            # loader; writes remain available and retain their normal approval.
            if "mcp_transport" in metadata and (
                metadata.get("hitl") is False or metadata.get("mcp_is_generic") is True
            ):
                denied.add(str(getattr(tool, "name", "")))

    denied.discard("")
    return denied


def retrieval_denial_reason(scope: RetrievalScope, *, subagent_name: str) -> str:
    """Why a retrieval tool is off this turn, worded so the model stops.

    Returned to the model in place of the denied call. It must not read like a
    file permission problem (the model then retries other ways); it says what
    the switch is, that retrying cannot help, and what to tell the user.
    """
    if subagent_name.startswith("knowledge_base"):
        return (
            f"this chat's retrieval scope is set to {scope.value!r}, which "
            "excludes the workspace knowledge base for this turn. No "
            "knowledge-base tool will work, so do not retry or rephrase. Tell "
            "the user once that reading their documents needs the Documents "
            "or All retrieval scope, and answer from what you have."
        )
    return (
        f"this chat's retrieval scope is set to {scope.value!r}, which "
        "excludes this source for this turn. Do not retry; tell the user "
        "once if the answer needs it."
    )


def sources_only_prompt(sources_only: SourcesOnly, scope: RetrievalScope) -> str:
    """The notebook-chat retrieval rule, placed last so it outranks the general
    platform guidance above it (which describes web specialists that a
    notebook chat does not have)."""

    lines = [
        "",
        "<retrieval_scope>",
        "This is a notebook chat: answer only from the notebook's enabled "
        "sources, through task(knowledge_base, ...). Retrieval never reaches "
        "the public web here: the web specialists named in the guidance above "
        "(Google Search, the web crawler, Reddit, YouTube and the other "
        "platforms) are not available, whatever that guidance says. Do not "
        "search the web or offer to as part of answering. If the sources do "
        "not cover the question, say so plainly and suggest adding sources "
        '(the notebook\'s "Search the web for new sources" box finds web '
        "pages to add), then answer only with what the sources support.",
    ]
    if scope.allows_web:
        lines.append(
            "The user @-mentioned a connected account in this message, so its "
            "connected app may be read for this message only; say that the "
            "information came from it, not from the notebook's sources."
        )
    else:
        lines.append(
            "Connected-app data is not retrieved either; connected-app actions "
            "remain available when explicitly requested."
        )
    for specialist in sorted(sources_only.picked_specialists):
        lines.append(
            f"For this message only, the user picked the {specialist} tool in "
            f"the composer, so task({specialist}, ...) is available. What it "
            "returns comes from the web, not from the notebook's sources, and "
            "is not added to them: say in the answer that it is from the web, "
            "keep it apart from what the sources say, and cite it only with "
            "the [n] its run returned, never with a source's [n]."
        )
    for specialist in sorted(sources_only.unavailable_picks):
        lines.append(
            f"For this message the user picked the {specialist} tool in the "
            "composer, but this workspace has no key for it, so it is not "
            "available and must not be called. Say so once in the answer, "
            "then answer from the sources."
        )
    lines.append("</retrieval_scope>")
    return "\n".join(lines) + "\n"


def retrieval_scope_prompt(
    scope: RetrievalScope, sources_only: SourcesOnly | None = None
) -> str:
    """Model-facing explanation; runtime deny rules remain authoritative."""

    if sources_only is not None:
        return sources_only_prompt(sources_only, scope)
    if scope is RetrievalScope.DOCUMENTS:
        return (
            "\n<retrieval_scope>Use only the workspace knowledge base for "
            "retrieval. Do not retrieve public web or connected-app data. "
            "Connected-app mutations remain available when explicitly requested."
            "</retrieval_scope>\n"
        )
    if scope is RetrievalScope.WEB:
        return (
            "\n<retrieval_scope>Use public web and connected apps for retrieval. "
            "Do not search or read the workspace knowledge base. Knowledge-base "
            "write actions remain available when explicitly requested."
            "</retrieval_scope>\n"
        )
    return (
        "\n<retrieval_scope>Workspace documents, public web, and connected apps "
        "are all available for retrieval.</retrieval_scope>\n"
    )


__all__ = [
    "RetrievalScope",
    "SourcesOnly",
    "denied_retrieval_tools",
    "excluded_retrieval_subagents",
    "retrieval_denial_reason",
    "retrieval_scope_prompt",
    "sources_only_prompt",
]
