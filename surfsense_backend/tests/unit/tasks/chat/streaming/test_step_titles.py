"""Sub-agent steps say which tool ran, not "Using a tool"."""

from __future__ import annotations

import pytest

from app.agents.chat.multi_agent_chat.subagents.connectors.email_imap.tools.index import (
    load_tools as load_email_tools,
)
from app.agents.chat.multi_agent_chat.subagents.shared.run_reader import (
    build_run_reader_tools,
)
from app.services.new_streaming_service import VercelStreamingService
from app.tasks.chat.content_builder import AssistantContentBuilder
from app.tasks.chat.streaming.handlers.custom_events import handle_activity_progress
from app.tasks.chat.streaming.handlers.tools.activity import resolve_tool_activity
from app.tasks.chat.streaming.relay.state import AgentEventRelayState

pytestmark = pytest.mark.unit


def _titles(tool, subagent_type: str) -> tuple[str, str]:
    spec = resolve_tool_activity(
        tool.name,
        subagent_type=subagent_type,
        trusted_descriptor=(tool.metadata or {}).get("activity_descriptor"),
    )
    return spec.active_title, spec.completed_title


def test_email_steps_name_what_they_do():
    tools = {
        tool.name: tool
        for tool in load_email_tools(db_session=None, workspace_id=1, user_id="u")
    }
    assert _titles(tools["search_emails"], "email_imap") == (
        "Searching email",
        "Searched email",
    )
    assert _titles(tools["read_email"], "email_imap") == (
        "Reading an email",
        "Read an email",
    )
    for tool in tools.values():
        assert _titles(tool, "email_imap")[0] != "Using a tool", tool.name


def test_saved_result_readers_name_what_they_do():
    tools = {tool.name: tool for tool in build_run_reader_tools(workspace_id=1)}
    assert _titles(tools["read_run"], "web_crawler") == (
        "Reading saved results",
        "Read saved results",
    )
    assert _titles(tools["search_run"], "web_crawler")[0] == "Searching saved results"
    assert _titles(tools["export_run"], "web_crawler")[0] == "Saving results as a file"


def test_crawl_progress_counts_pages_on_the_running_step():
    state = AgentEventRelayState()
    spec = resolve_tool_activity("web_crawl", subagent_type="web_crawler")
    snapshot = spec.snapshot(
        activity_id="act_turn_1",
        sequence=1,
        status="running",
        started_at="2026-10-09T17:29:37+00:00",
    )
    state.journal.spec_by_id[snapshot["id"]] = spec
    state.journal.snapshot_by_id[snapshot["id"]] = snapshot

    frame = handle_activity_progress(
        {"phase": "crawled", "current": 2, "total": 3, "url": "https://x.test/a"},
        state=state,
        streaming_service=VercelStreamingService(),
        content_builder=AssistantContentBuilder(),
    )

    assert frame is not None
    assert '"progressTitle": "Reading pages (2/3)"' in frame
    assert "x.test" not in frame
