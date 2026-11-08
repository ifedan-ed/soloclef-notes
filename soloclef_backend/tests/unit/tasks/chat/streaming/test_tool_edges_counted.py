"""Every tool call start and end is counted, so other pages see the step at once."""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.services.new_streaming_service import VercelStreamingService
from app.tasks.chat.content_builder import AssistantContentBuilder
from app.tasks.chat.streaming.handlers.tool_end import iter_tool_end_frames
from app.tasks.chat.streaming.handlers.tool_start import iter_tool_start_frames
from app.tasks.chat.streaming.relay.state import AgentEventRelayState

pytestmark = pytest.mark.unit


def _start(state, builder, service, name: str, run_id: str) -> None:
    list(
        iter_tool_start_frames(
            {"name": name, "run_id": run_id, "data": {"input": {}}},
            state=state,
            streaming_service=service,
            content_builder=builder,
            result=SimpleNamespace(write_attempted=False),
            step_prefix="turn",
        )
    )


def _end(state, builder, service, name: str, run_id: str) -> None:
    list(
        iter_tool_end_frames(
            {
                "name": name,
                "run_id": run_id,
                "data": {
                    "output": SimpleNamespace(content="done", tool_call_id=run_id)
                },
            },
            state=state,
            streaming_service=service,
            content_builder=builder,
            result=SimpleNamespace(write_attempted=False),
            step_prefix="turn",
            config={},
        )
    )


def test_a_specialists_tool_calls_are_counted_like_the_main_agents():
    service = VercelStreamingService()
    builder = AssistantContentBuilder()
    state = AgentEventRelayState()

    _start(state, builder, service, "task", "run-task")  # the specialist
    assert state.tool_runs_started_or_ended == 1
    _start(state, builder, service, "web_crawl", "run-crawl")  # its page fetch
    assert state.active_tool_depth == 2
    assert state.tool_runs_started_or_ended == 2
    _end(state, builder, service, "web_crawl", "run-crawl")
    _end(state, builder, service, "task", "run-task")
    assert state.tool_runs_started_or_ended == 4
