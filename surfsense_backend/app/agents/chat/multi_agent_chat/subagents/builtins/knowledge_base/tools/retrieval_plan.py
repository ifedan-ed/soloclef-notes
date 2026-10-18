"""Decide which documents a knowledge-base search may reach.

Two inputs shape a search besides the workspace's document-type filter:

* ``@``-mentions are a per-turn instruction ("look at this"): retrieval is
  confined to them.
* The turn's document scope (a notebook chat's enabled sources, folders
  expanded) is a hard limit: nothing outside it is searched. Mentions are
  intersected with it; a mention that falls outside it is ignored, and when
  none remain the whole scope is searched.

Without a scope (plain chats, automations) behaviour is unchanged: mentions
confine the search, otherwise the whole knowledge base is searched.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass

from app.agents.chat.multi_agent_chat.shared.retrieval import SearchScope


@dataclass(frozen=True)
class RetrievalPlan:
    # ``None`` means there is nothing the turn may search (an empty scope).
    scope: SearchScope | None


def plan_retrieval(
    *,
    document_types: tuple[str, ...] | None,
    mentioned_document_ids: Sequence[int],
    document_scope: frozenset[int] | None,
) -> RetrievalPlan:
    """``mentioned_document_ids`` is already expanded from folders."""
    mentioned = set(mentioned_document_ids)
    if document_scope is not None:
        if not document_scope:
            return RetrievalPlan(scope=None)
        allowed = (mentioned & document_scope) or set(document_scope)
        return RetrievalPlan(
            scope=SearchScope(
                document_types=document_types, document_ids=tuple(sorted(allowed))
            )
        )
    if mentioned:
        return RetrievalPlan(
            scope=SearchScope(
                document_types=document_types, document_ids=tuple(sorted(mentioned))
            )
        )
    return RetrievalPlan(
        scope=SearchScope(document_types=document_types, document_ids=None)
    )
