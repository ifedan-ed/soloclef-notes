"""A search of several sources lands in the runs log as one row per source."""

from __future__ import annotations

import asyncio

import pytest

from app.notebooks.research.multi_run_log import logged_multi_research_events
from app.notebooks.research.schemas import SearchRequest

pytestmark = pytest.mark.unit

REQUEST = SearchRequest(query="hfnc", sources=["pubmed", "web", "reddit"], mode="fast")
COSTS = {"pubmed": 0, "web": 250, "reddit": 900}


async def _stream(*events):
    for event in events:
        yield event


def _logged(events, recorded):
    async def record(**kwargs):
        recorded.append(kwargs)
        return "run-id"

    return logged_multi_research_events(
        events,
        request=REQUEST,
        record=record,
        workspace_id=7,
        user_id=None,
        notebook_id=3,
        cost_micros_for=COSTS.__getitem__,
    )


def _provider(source, status, **extra):
    return {"type": "provider", "source": source, "status": status, **extra}


async def test_each_source_gets_its_own_row_with_its_results_status_and_cost():
    recorded = []
    rows = [
        {"id": "p1", "url": "https://pubmed/1", "source": "pubmed"},
        {"id": "w1", "url": "https://w/1", "source": "web"},
        {"id": "p2", "url": "https://pubmed/2", "source": "pubmed"},
    ]
    events = _logged(
        _stream(
            {"type": "phase", "phase": "searching", "progress": 5},
            _provider("pubmed", "searching"),
            _provider("pubmed", "done", count=2),
            _provider("web", "done", count=1),
            _provider("reddit", "error", code="source_busy", message="Reddit is busy"),
            {"type": "results", "results": rows},
            {"type": "done"},
        ),
        recorded,
    )
    passed = [e async for e in events]
    assert passed[-1] == {"type": "done"}
    by_source = {run["input"]["source"]: run for run in recorded}
    assert [run["capability"] for run in recorded] == [
        "research.pubmed",
        "research.web",
        "research.reddit",
    ]
    assert (
        by_source["pubmed"]["status"],
        by_source["pubmed"]["serialized"].item_count,
    ) == (
        "success",
        2,
    )
    assert by_source["web"]["cost_micros"] == 250
    assert (by_source["reddit"]["status"], by_source["reddit"]["error"]) == (
        "error",
        "Reddit is busy",
    )
    assert by_source["reddit"]["cost_micros"] == 900
    assert by_source["web"]["input"] == {
        "query": "hfnc",
        "source": "web",
        "sources": ["pubmed", "web", "reddit"],
        "mode": "fast",
        "notebook_id": 3,
    }


async def test_a_search_stopped_midway_records_unfinished_sources_as_cancelled():
    recorded = []
    events = _logged(
        _stream(
            _provider("pubmed", "error", code="source_busy", message="busy"),
            _provider("web", "done", count=4),
            {"type": "phase", "phase": "reading", "progress": 40},
        ),
        recorded,
    )
    await anext(events)
    await anext(events)
    await events.aclose()
    for _ in range(3):
        await asyncio.sleep(0)
    statuses = {run["input"]["source"]: run["status"] for run in recorded}
    assert statuses == {"pubmed": "error", "web": "cancelled", "reddit": "cancelled"}
