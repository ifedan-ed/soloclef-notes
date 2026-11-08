"""Parsing a model's reply into a structured shape.

Agent LLMs wrap JSON in prose and markdown fences. ``invoke_json`` exists so
every node that asks a model for structured data tolerates that the same way.
The LLM is an external boundary, so it is faked with a canned reply; the
behavior under test is the parsing, not the model.
"""

from __future__ import annotations

import asyncio
import json

import pytest
from langchain_core.messages import AIMessage, HumanMessage
from pydantic import BaseModel, ValidationInfo, model_validator

from app.utils.structured_output import (
    StructuredOutputError,
    flatten_to_text,
    invoke_json,
)

pytestmark = pytest.mark.unit


class _Shape(BaseModel):
    name: str
    count: int


class _CannedLLM:
    """A TTS-free stand-in for the chat model: replies with one fixed string."""

    def __init__(self, reply: str) -> None:
        self._reply = reply

    async def ainvoke(self, _messages):
        return SimpleReply(self._reply)


class SimpleReply:
    def __init__(self, content: str) -> None:
        self.content = content


async def _parse(reply: str) -> _Shape:
    return await invoke_json(_CannedLLM(reply), [], _Shape)


async def test_parses_a_clean_json_reply():
    shape = await _parse('{"name": "alpha", "count": 3}')
    assert shape == _Shape(name="alpha", count=3)


async def test_parses_json_wrapped_in_a_markdown_fence():
    reply = '```json\n{"name": "beta", "count": 7}\n```'
    shape = await _parse(reply)
    assert shape == _Shape(name="beta", count=7)


async def test_extracts_json_embedded_in_prose():
    """Reasoning models prepend/append chatter around the object."""
    reply = 'Sure, here you go: {"name": "gamma", "count": 1} — hope that helps!'
    shape = await _parse(reply)
    assert shape == _Shape(name="gamma", count=1)


async def test_parses_literal_control_characters_inside_model_strings():
    shape = await _parse('{"name": "first line\nsecond line", "count": 2}')
    assert shape == _Shape(name="first line\nsecond line", count=2)


async def test_raises_when_there_is_no_json_object():
    with pytest.raises(StructuredOutputError):
        await _parse("I could not produce that.")


async def test_raises_when_the_json_does_not_match_the_shape():
    with pytest.raises(StructuredOutputError):
        await _parse('{"name": "delta"}')


class _ScriptedLLM:
    """Replies from a script, recording every conversation it was sent."""

    def __init__(self, *replies: str, delay: float = 0.0) -> None:
        self._replies = list(replies)
        self._delay = delay
        self.calls: list[list] = []

    async def ainvoke(self, messages):
        self.calls.append(list(messages))
        if self._delay:
            await asyncio.sleep(self._delay)
        return SimpleReply(self._replies.pop(0))


async def test_a_rejected_reply_is_re_asked_with_the_reply_and_the_error():
    llm = _ScriptedLLM('{"name": "delta"}', '{"name": "delta", "count": 4}')
    prompt = [HumanMessage(content="give me a shape")]

    shape = await invoke_json(llm, prompt, _Shape)

    assert shape == _Shape(name="delta", count=4)
    assert len(llm.calls) == 2
    original, rejected, correction = llm.calls[1]
    assert original is prompt[0]
    assert isinstance(rejected, AIMessage)
    assert rejected.content == '{"name": "delta"}'
    assert isinstance(correction, HumanMessage)
    assert "count: Field required" in correction.content


async def test_the_retry_reports_prose_as_no_json_object():
    llm = _ScriptedLLM("I cannot.", '{"name": "x", "count": 1}')

    await invoke_json(llm, [], _Shape)

    assert "no JSON object" in llm.calls[1][-1].content


async def test_gives_up_after_exactly_one_retry():
    llm = _ScriptedLLM('{"name": "a"}', '{"count": 2}', '{"name": "c", "count": 3}')

    with pytest.raises(StructuredOutputError) as raised:
        await invoke_json(llm, [], _Shape)

    assert len(llm.calls) == 2
    assert "after one retry" in str(raised.value)
    assert "name: Field required" in str(raised.value)


async def test_the_error_does_not_echo_the_offending_input():
    secret_sized = "x" * 5000
    llm = _ScriptedLLM(*[json.dumps({"name": secret_sized, "count": "many"})] * 2)

    with pytest.raises(StructuredOutputError) as raised:
        await invoke_json(llm, [], _Shape)

    assert secret_sized not in str(raised.value)
    assert "count" in str(raised.value)


class _Counted(BaseModel):
    items: list[int]

    @model_validator(mode="after")
    def expected_count(self, info: ValidationInfo) -> _Counted:
        expected = (info.context or {}).get("expected")
        if expected is not None and len(self.items) != expected:
            raise ValueError(f"expected {expected} items, got {len(self.items)}")
        return self


async def test_validation_context_rules_are_retried_like_schema_errors():
    llm = _ScriptedLLM('{"items": [1]}', '{"items": [1, 2]}')

    result = await invoke_json(llm, [], _Counted, validation_context={"expected": 2})

    assert result.items == [1, 2]
    assert "expected 2 items, got 1" in llm.calls[1][-1].content


async def test_each_attempt_gets_its_own_timeout():
    # Each ask takes 0.15 s against a 0.25 s budget: together they exceed it,
    # so a single shared deadline would time the retry out.
    llm = _ScriptedLLM('{"name": "a"}', '{"name": "a", "count": 1}', delay=0.15)

    shape = await invoke_json(llm, [], _Shape, attempt_timeout=0.25)

    assert shape.count == 1


async def test_a_hung_attempt_times_out_without_a_retry():
    llm = _ScriptedLLM('{"name": "a", "count": 1}', delay=5)

    with pytest.raises(TimeoutError):
        await invoke_json(llm, [], _Shape, attempt_timeout=0.05)

    assert len(llm.calls) == 1


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("plain", "plain"),
        (None, ""),
        ({"issue": "Footer clipped", "page": 3}, "issue: Footer clipped; page: 3"),
        (["a", {"b": 1}, None], "a; b: 1"),
        (7, "7"),
    ],
)
def test_flatten_to_text(value, expected):
    assert flatten_to_text(value) == expected


async def test_a_reasoning_models_thinking_blocks_are_not_parsed():
    """DeepSeek replies [thinking, text]; the video still review reads only the text."""
    reply = [
        {"type": "thinking", "thinking": 'Maybe {"name": "wrong", "count": 0}?'},
        {"type": "text", "text": '{"name": "gamma", "count": 2}'},
    ]
    shape = await invoke_json(_CannedLLM(reply), [], _Shape)
    assert shape == _Shape(name="gamma", count=2)
