"""Checkpoint serializer: SurfSense's citation types load in strict mode.

Without an allowlist langgraph rebuilds any class but warns that unregistered
types "will be blocked in a future version"; once blocked, a stored
``CitationRegistry`` would come back as a plain dict. The registered
serializer is strict now, so these tests prove it behaves the way a future
langgraph will: registered types round-trip, unregistered ones do not.
"""

from __future__ import annotations

import importlib
import logging

import pytest
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage
from langgraph.checkpoint.base import empty_checkpoint
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.checkpoint.serde.event_hooks import register_serde_event_listener
from langgraph.types import Overwrite, Send
from pydantic import BaseModel

from app.agents.chat.multi_agent_chat.shared.citations import (
    CitationRegistry,
    CitationSourceType,
)
from app.agents.chat.runtime.checkpoint_serde import (
    SURFSENSE_CHECKPOINT_TYPES,
    checkpoint_serde,
)

pytestmark = pytest.mark.unit


class NotRegistered(BaseModel):
    value: int


def _registry() -> CitationRegistry:
    registry = CitationRegistry()
    registry.register(CitationSourceType.KB_CHUNK, {"chunk_id": 42}, {"title": "Deck"})
    registry.register(
        CitationSourceType.RUN, {"run_id": "run_1234"}, {"capability": "x"}
    )
    registry.register(CitationSourceType.WEB_RESULT, {"url": "https://example.org"})
    return registry


@pytest.fixture
def serde_events():
    events: list[dict] = []
    unregister = register_serde_event_listener(events.append)
    yield events
    unregister()


def test_every_registered_type_is_a_real_class():
    for module, name in SURFSENSE_CHECKPOINT_TYPES:
        assert isinstance(getattr(importlib.import_module(module), name), type)
    # The registry and the enum are named exactly as they serialize.
    registry_key = (CitationRegistry.__module__, CitationRegistry.__name__)
    enum_key = (CitationSourceType.__module__, CitationSourceType.__name__)
    assert {registry_key, enum_key} <= SURFSENSE_CHECKPOINT_TYPES


def test_the_serializer_is_strict():
    assert checkpoint_serde()._allowed_msgpack_modules is not True


def test_citation_state_round_trips_in_strict_mode(serde_events, caplog):
    serde = checkpoint_serde()
    state = {
        "citation_registry": _registry(),
        "messages": [
            HumanMessage(content="q"),
            AIMessage(content="a [1]"),
            ToolMessage(content="r", tool_call_id="c1"),
        ],
        "send": Send("task", {"x": 1}),
        "overwrite": Overwrite([1]),
    }

    with caplog.at_level(logging.WARNING, logger="langgraph.checkpoint.serde.jsonplus"):
        loaded = serde.loads_typed(serde.dumps_typed(state))

    assert isinstance(loaded["citation_registry"], CitationRegistry)
    assert loaded["citation_registry"] == state["citation_registry"]
    entry = loaded["citation_registry"].resolve(2)
    assert entry.source_type is CitationSourceType.RUN
    assert loaded["messages"] == state["messages"]
    assert loaded["send"] == state["send"]
    assert serde_events == []
    assert "unregistered" not in caplog.text and "Blocked" not in caplog.text


def test_an_unregistered_type_is_blocked(serde_events):
    serde = checkpoint_serde()

    loaded = serde.loads_typed(serde.dumps_typed({"x": NotRegistered(value=1)}))

    assert loaded["x"] == {"value": 1}
    assert [e["kind"] for e in serde_events] == ["msgpack_blocked"]


async def test_a_checkpoint_with_citations_round_trips_through_a_saver(serde_events):
    saver = InMemorySaver(serde=checkpoint_serde())
    checkpoint = empty_checkpoint()
    checkpoint["channel_values"] = {"citation_registry": _registry()}
    checkpoint["channel_versions"] = {"citation_registry": 1}
    config = {"configurable": {"thread_id": "t", "checkpoint_ns": ""}}

    saved = await saver.aput(config, checkpoint, {}, {"citation_registry": 1})
    await saver.aput_writes(saved, [("citation_registry", _registry())], "task-1")
    loaded = await saver.aget_tuple(saved)

    registry = loaded.checkpoint["channel_values"]["citation_registry"]
    assert isinstance(registry, CitationRegistry)
    assert registry == _registry()
    assert isinstance(loaded.pending_writes[0][2], CitationRegistry)
    assert serde_events == []
