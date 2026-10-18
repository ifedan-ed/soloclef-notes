"""A failed tool inside a chat turn leaves a WARNING line in the backend log."""

from __future__ import annotations

import asyncio
import json
import logging
from types import SimpleNamespace

import pytest
from langchain_core.messages import ToolMessage

from app.observability.logs import get_log_context, log_context
from app.observability.logs.chat import bind_chat_turn
from app.services.new_streaming_service import VercelStreamingService
from app.tasks.chat.content_builder import AssistantContentBuilder
from app.tasks.chat.streaming.handlers.tool_end import iter_tool_end_frames
from app.tasks.chat.streaming.handlers.tool_start import iter_tool_start_frames
from app.tasks.chat.streaming.relay.event_relay import EventRelay
from app.tasks.chat.streaming.relay.state import AgentEventRelayState

pytestmark = pytest.mark.unit

CONFIG = {"configurable": {"thread_id": "86", "turn_id": "86:1790299829778"}}


def _result() -> SimpleNamespace:
    return SimpleNamespace(
        write_attempted=False,
        write_succeeded=False,
        verification_succeeded=False,
        sandbox_files=[],
    )


def _run_tool(name: str, output, *, state=None) -> None:
    service = VercelStreamingService()
    builder = AssistantContentBuilder()
    state = state or AgentEventRelayState()
    result = _result()
    list(
        iter_tool_start_frames(
            {
                "name": name,
                "run_id": "run-1",
                "data": {"input": {"q": "private query"}},
            },
            state=state,
            streaming_service=service,
            content_builder=builder,
            result=result,
            step_prefix="turn",
        )
    )
    list(
        iter_tool_end_frames(
            {"name": name, "run_id": "run-1", "data": {"output": output}},
            state=state,
            streaming_service=service,
            content_builder=builder,
            result=result,
            step_prefix="turn",
            config=CONFIG,
        )
    )


def _tool_records(caplog) -> list[logging.LogRecord]:
    return [r for r in caplog.records if r.name == "surfsense.chat.tools"]


def test_error_payload_logs_tool_type_thread_and_short_message(caplog) -> None:
    output = {
        "error": "Upstream 503 from provider api_key=sk-abcdefghijklmnopqrstuv",
        "error_type": "provider_unavailable",
    }
    with caplog.at_level(logging.WARNING, logger="surfsense.chat.tools"):
        _run_tool("web_search", output)
    [rec] = _tool_records(caplog)
    msg = rec.getMessage()
    assert rec.levelno == logging.WARNING
    assert "tool=web_search" in msg
    assert "type=provider_unavailable" in msg
    assert "thread=86" in msg
    assert "Upstream 503" in msg
    # The tool input is never logged.
    assert "private query" not in msg


def test_error_tool_message_status_is_logged(caplog) -> None:
    msg = ToolMessage(
        content="Error: file not found", tool_call_id="c1", status="error"
    )
    state = AgentEventRelayState(active_subagent_type="knowledge_base")
    with caplog.at_level(logging.WARNING, logger="surfsense.chat.tools"):
        _run_tool("read_file", msg, state=state)
    [rec] = _tool_records(caplog)
    assert "tool=read_file subagent=knowledge_base" in rec.getMessage()
    assert "file not found" in rec.getMessage()


def test_successful_tool_logs_nothing(caplog) -> None:
    with caplog.at_level(logging.WARNING, logger="surfsense.chat.tools"):
        _run_tool("web_search", {"status": "success", "results": [1, 2]})
    assert _tool_records(caplog) == []


def test_long_errors_are_flattened_and_capped(caplog) -> None:
    output = {"error": "line one\nline two " + "x" * 2000}
    with caplog.at_level(logging.WARNING, logger="surfsense.chat.tools"):
        _run_tool("execute", output)
    [rec] = _tool_records(caplog)
    assert "\n" not in rec.getMessage()
    assert len(rec.getMessage()) < 500


def test_raised_tool_error_event_is_logged(caplog) -> None:
    async def events():
        yield {
            "event": "on_tool_error",
            "name": "scrape",
            "data": {"error": TimeoutError("took too long")},
        }

    async def consume() -> None:
        relay = EventRelay(streaming_service=VercelStreamingService())
        async for _ in relay.relay(
            events(), state=AgentEventRelayState(), result=_result(), config=CONFIG
        ):
            pass

    with caplog.at_level(logging.WARNING, logger="surfsense.chat.tools"):
        asyncio.run(consume())
    [rec] = _tool_records(caplog)
    assert "tool raised: tool=scrape type=TimeoutError" in rec.getMessage()
    assert "took too long" in rec.getMessage()


def test_turn_ids_bind_into_the_log_context() -> None:
    with log_context(request_id="req_000000000abc"):
        bind_chat_turn(CONFIG)
        ctx = get_log_context()
    assert ctx["thread_id"] == "86"
    assert ctx["turn_id"] == "86:1790299829778"
    assert json.dumps(ctx)
