"""The checkpoint serializer, with SurfSense's own state types registered.

LangGraph's ``JsonPlusSerializer`` writes non-JSON values in graph state as
msgpack "ext" records naming the class to rebuild (``module``, ``name``). With
no allowlist it rebuilds any class but logs "Deserializing unregistered type
... This will be blocked in a future version": a later langgraph would stop
rebuilding them and hand back plain dicts, and chats whose checkpoints hold a
``CitationRegistry`` could no longer be resumed or reloaded as before.

Passing an explicit allowlist switches the serializer to strict mode today:
the built-in safe types (messages, datetimes, ``Send``/``Interrupt``/
``Command`` …, see ``langgraph.checkpoint.serde._msgpack.SAFE_MSGPACK_TYPES``)
plus exactly the types below. Anything else is not rebuilt (it comes back as
its raw fields) and langgraph logs "Blocked deserialization of …".

The list was taken from every row of the live checkpoint tables (see
``scripts/verify_checkpoint_serde.py``, which decodes all of them with this
config and reports failures). When a new class starts landing in graph state,
add it here and rerun that script before deploying.
"""

from __future__ import annotations

from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer

# (module, class) pairs SurfSense stores in LangGraph state, beyond
# langgraph's built-in safe types.
SURFSENSE_CHECKPOINT_TYPES: frozenset[tuple[str, str]] = frozenset(
    {
        # The per-conversation [n] <-> source map (citation_registry channel).
        (
            "app.agents.chat.multi_agent_chat.shared.citations.registry",
            "CitationRegistry",
        ),
        (
            "app.agents.chat.multi_agent_chat.shared.citations.models",
            "CitationEntry",
        ),
        (
            "app.agents.chat.multi_agent_chat.shared.citations.models",
            "CitationSourceType",
        ),
    }
)


def checkpoint_serde() -> JsonPlusSerializer:
    """A strict serializer that also rebuilds SurfSense's registered types."""

    return JsonPlusSerializer(allowed_msgpack_modules=SURFSENSE_CHECKPOINT_TYPES)


__all__ = ["SURFSENSE_CHECKPOINT_TYPES", "checkpoint_serde"]
