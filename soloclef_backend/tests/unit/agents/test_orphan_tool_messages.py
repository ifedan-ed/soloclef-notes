"""A tool result whose call was compacted away must not reach the model."""

import asyncio

import pytest
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

from app.agents.chat.multi_agent_chat.shared.middleware.patch_tool_calls import (
    SequenceRepairMiddleware,
    build_patch_tool_calls_mw,
    drop_orphan_tool_messages,
)

pytestmark = pytest.mark.unit


def _call(cid: str) -> AIMessage:
    return AIMessage(content="", tool_calls=[{"id": cid, "name": "search", "args": {}}])


def test_orphan_result_is_dropped_and_answered_ones_kept():
    msgs = [
        HumanMessage("hi"),
        ToolMessage("left behind by compaction", tool_call_id="gone"),
        _call("a"),
        ToolMessage("answer a", tool_call_id="a"),
        HumanMessage("next"),
    ]
    out = drop_orphan_tool_messages(msgs)
    assert [type(m).__name__ for m in out] == ["HumanMessage", "AIMessage", "ToolMessage", "HumanMessage"]
    assert out[2].tool_call_id == "a"


def test_result_before_its_call_counts_as_orphan():
    msgs = [ToolMessage("too early", tool_call_id="b"), _call("b"), ToolMessage("ok", tool_call_id="b")]
    out = drop_orphan_tool_messages(msgs)
    assert len(out) == 2 and isinstance(out[0], AIMessage)


def test_clean_history_is_unchanged():
    msgs = [HumanMessage("q"), _call("c"), ToolMessage("r", tool_call_id="c")]
    assert drop_orphan_tool_messages(msgs) == msgs


class _Req:
    def __init__(self, messages):
        self.messages = messages

    def override(self, messages):
        return _Req(messages)


def test_middleware_sends_the_repaired_list_to_the_model():
    mw = build_patch_tool_calls_mw()
    assert isinstance(mw, SequenceRepairMiddleware)
    seen = {}

    async def handler(req):
        seen["messages"] = req.messages
        return "ok"

    req = _Req([HumanMessage("q"), ToolMessage("orphan", tool_call_id="x")])
    assert asyncio.run(mw.awrap_model_call(req, handler)) == "ok"
    assert [type(m).__name__ for m in seen["messages"]] == ["HumanMessage"]
    assert mw.wrap_model_call(req, lambda r: len(r.messages)) == 1
