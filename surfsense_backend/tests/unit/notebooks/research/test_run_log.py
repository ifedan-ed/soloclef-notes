"""Every research search lands in the runs log with how it ended."""

from __future__ import annotations

import asyncio
import json

import pytest

from app.notebooks.research.run_log import logged_research_events
from app.notebooks.research.schemas import SearchRequest

pytestmark = pytest.mark.unit

REQUEST = SearchRequest(query="hfnc", source="pubmed", mode="deep")


async def _stream(*events):
    for event in events:
        yield event


def _logged(events, recorded, cost=0):
    async def record(**kwargs):
        recorded.append(kwargs)
        return "run-id"

    return logged_research_events(
        events,
        request=REQUEST,
        record=record,
        workspace_id=7,
        user_id=None,
        notebook_id=3,
        cost_micros=lambda: cost,
    )


async def test_a_finished_search_is_recorded_with_its_results_and_cost():
    recorded = []
    rows = [{"id": "a", "url": "https://x/1"}, {"id": "b", "url": "https://x/2"}]
    events = _logged(
        _stream(
            {"type": "phase", "phase": "searching", "progress": 5},
            {"type": "results", "results": rows},
            {"type": "done"},
        ),
        recorded,
        cost=1234,
    )
    passed = [e async for e in events]
    assert [e["type"] for e in passed] == ["phase", "results", "done"]
    [run] = recorded
    assert run["capability"] == "research.pubmed"
    assert run["origin"] == "notebook"
    assert run["status"] == "success"
    assert run["input"] == {
        "query": "hfnc",
        "source": "pubmed",
        "mode": "deep",
        "notebook_id": 3,
    }
    assert run["serialized"].item_count == 2
    assert json.loads(run["serialized"].text.splitlines()[0])["id"] == "a"
    assert run["cost_micros"] == 1234
    assert run["duration_ms"] >= 0
    assert run["progress"] == [{"phase": "searching", "progress": 5}]
    assert run["workspace_id"] == 7


async def test_an_error_event_is_recorded_as_an_error():
    recorded = []
    events = _logged(
        _stream({"type": "error", "code": "source_blocked", "message": "walled"}),
        recorded,
    )
    [e async for e in events]
    assert recorded[0]["status"] == "error"
    assert recorded[0]["error"] == "walled"
    assert recorded[0]["serialized"].item_count == 0


async def test_a_search_abandoned_midway_is_recorded_as_cancelled():
    recorded = []
    events = _logged(
        _stream(
            {"type": "phase", "phase": "searching", "progress": 5},
            {"type": "phase", "phase": "reading", "progress": 40},
        ),
        recorded,
    )
    await anext(events)
    await events.aclose()
    await asyncio.sleep(0)  # the recording runs detached from the closed stream
    await asyncio.sleep(0)
    assert recorded[0]["status"] == "cancelled"
