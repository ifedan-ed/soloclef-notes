"""The production Postgres checkpointer stores and reloads citations in strict mode.

Uses ``get_checkpointer()`` itself (the saver chat turns use), so the test
fails if the saver is ever built without SurfSense's registered types again.
"""

from __future__ import annotations

import uuid

import pytest
from langchain_core.messages import AIMessage, HumanMessage
from langgraph.checkpoint.base import empty_checkpoint
from langgraph.checkpoint.serde.event_hooks import register_serde_event_listener

from app.agents.chat.multi_agent_chat.shared.citations import (
    CitationRegistry,
    CitationSourceType,
)
from app.agents.chat.runtime import checkpointer as checkpointer_module

pytestmark = pytest.mark.integration


async def test_a_checkpoint_with_citations_reloads_from_postgres_in_strict_mode():
    events: list[dict] = []
    unregister = register_serde_event_listener(events.append)
    saver = await checkpointer_module.get_checkpointer()
    try:
        assert saver.serde._allowed_msgpack_modules is not True  # strict

        registry = CitationRegistry()
        registry.register(CitationSourceType.KB_CHUNK, {"chunk_id": 7})
        registry.register(CitationSourceType.RUN, {"run_id": "run_abc"})
        checkpoint = empty_checkpoint()
        checkpoint["channel_values"] = {
            "citation_registry": registry,
            "messages": [HumanMessage(content="q"), AIMessage(content="a [1] [2]")],
        }
        checkpoint["channel_versions"] = {"citation_registry": 1, "messages": 1}
        config = {
            "configurable": {"thread_id": f"serde-{uuid.uuid4()}", "checkpoint_ns": ""}
        }

        saved = await saver.aput(
            config,
            checkpoint,
            {"source": "loop", "step": 1},
            {"citation_registry": 1, "messages": 1},
        )
        await saver.aput_writes(saved, [("citation_registry", registry)], "task-1")
        loaded = await saver.aget_tuple(saved)

        values = loaded.checkpoint["channel_values"]
        assert isinstance(values["citation_registry"], CitationRegistry)
        assert values["citation_registry"] == registry
        assert (
            values["citation_registry"].resolve(2).source_type is CitationSourceType.RUN
        )
        assert values["messages"] == checkpoint["channel_values"]["messages"]
        assert isinstance(loaded.pending_writes[0][2], CitationRegistry)
        assert events == []

        await saver.adelete_thread(config["configurable"]["thread_id"])
    finally:
        unregister()
        await checkpointer_module.close_checkpointer()
