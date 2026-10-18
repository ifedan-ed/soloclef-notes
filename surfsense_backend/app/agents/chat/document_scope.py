"""The set of documents one chat turn may search, list or read.

A notebook chat answers only from its notebook's enabled sources, so the turn
carries those document ids as a hard scope. Every path that fetches document
content (``search_knowledge_base``, the ``/documents`` filesystem tools, the
workspace tree) enforces it at the query, not in the prompt.

``None`` means unrestricted (plain chats, automations). An empty scope means
nothing may be read.

The scope travels two ways, both server-side:

* ``runtime.context.document_scope`` on the main graph, and
* ``config["configurable"][CONFIG_KEY]``, which every nested subagent
  invocation inherits (``subagent_invoke_config`` copies the parent config),
  so it reaches the knowledge-base subagent and ``ask_knowledge_base`` without
  depending on each subagent's state schema.

When both are present they are intersected, so neither can widen the other.
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any

CONFIG_KEY = "surfsense_document_scope"

DocumentScope = frozenset[int] | None


def _as_scope(value: Any) -> DocumentScope:
    if value is None:
        return None
    if isinstance(value, (list, tuple, set, frozenset)):
        return frozenset(int(v) for v in value)
    return None


def _config_of(runtime: Any) -> dict[str, Any] | None:
    config = getattr(runtime, "config", None)
    if isinstance(config, dict):
        return config
    try:
        from langgraph.config import get_config

        return get_config()
    except Exception:
        return None


def document_scope_of(runtime: Any) -> DocumentScope:
    """The turn's document scope as seen from a tool or middleware ``runtime``."""
    scopes: list[frozenset[int]] = []
    ctx_scope = _as_scope(
        getattr(getattr(runtime, "context", None), "document_scope", None)
    )
    if ctx_scope is not None:
        scopes.append(ctx_scope)
    config = _config_of(runtime)
    configurable = (config or {}).get("configurable") or {}
    config_scope = _as_scope(configurable.get(CONFIG_KEY))
    if config_scope is not None:
        scopes.append(config_scope)
    if not scopes:
        return None
    result = scopes[0]
    for scope in scopes[1:]:
        result = result & scope
    return result


def in_scope(scope: DocumentScope, document_id: int | None) -> bool:
    """Whether ``document_id`` may be read under ``scope``."""
    if scope is None:
        return True
    return document_id is not None and document_id in scope


def restrict(scope: DocumentScope, document_ids: Iterable[int]) -> list[int]:
    """``document_ids`` limited to ``scope`` (order kept)."""
    if scope is None:
        return list(document_ids)
    return [doc_id for doc_id in document_ids if doc_id in scope]


def scope_config_value(scope: Iterable[int] | None) -> list[int] | None:
    """The ``configurable`` value for a scope (sorted list, or ``None``)."""
    return None if scope is None else sorted({int(v) for v in scope})


__all__ = [
    "CONFIG_KEY",
    "DocumentScope",
    "document_scope_of",
    "in_scope",
    "restrict",
    "scope_config_value",
]
