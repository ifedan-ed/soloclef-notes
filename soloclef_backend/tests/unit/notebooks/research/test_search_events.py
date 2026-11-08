"""A research search narrates itself: phases, then results and done, or one error."""

from __future__ import annotations

import pytest

from app.capabilities.google_search.scrape.schemas import ScrapeOutput as SerpOutput
from app.capabilities.reddit.scrape.schemas import ScrapeOutput as RedditOutput
from app.exceptions import ForbiddenError
from app.notebooks.research.schemas import SearchRequest
from app.notebooks.research.search import OUT_OF_CREDIT, research_events
from app.proprietary.platforms.google_search import SerpItem
from app.services.etl_credit_service import InsufficientCreditsError

pytestmark = pytest.mark.unit


def _serp(count: int, prefix: str = "a") -> SerpItem:
    return SerpItem.model_validate(
        {
            "organicResults": [
                {"title": f"{prefix}{i}", "url": f"https://{prefix}{i}.example/"}
                for i in range(count)
            ]
        }
    )


class FakeRunner:
    def __init__(self, outputs=None, error=None):
        self.outputs = outputs or {}
        self.error = error
        self.calls = []

    async def run(self, capability, payload):
        self.calls.append((capability.name, payload))
        if self.error:
            raise self.error
        return self.outputs[capability.name]


async def _no_searxng(query, page):
    return None


async def _no_read(url):
    return None


async def _events(request, runner, reader=_no_read, searxng=_no_searxng):
    return [e async for e in research_events(request, runner, reader, searxng)]


async def test_fast_web_search_uses_searxng_first():
    pages = []

    async def searxng(query, page):
        pages.append(page)
        return _serp(12)

    runner = FakeRunner()
    events = await _events(SearchRequest(query="solar"), runner, searxng=searxng)
    assert [e["type"] for e in events] == ["phase", "phase", "results", "done"]
    assert events[0] == {"type": "phase", "phase": "searching", "progress": 5}
    assert len(events[2]["results"]) == 10
    assert pages == [1]
    assert runner.calls == []


async def test_web_falls_back_to_google_when_searxng_is_off():
    runner = FakeRunner({"google_search.scrape": SerpOutput(items=[_serp(3)])})
    events = await _events(SearchRequest(query="solar"), runner)
    [(name, payload)] = runner.calls
    assert name == "google_search.scrape"
    assert payload.queries == ["solar"]
    assert {r["source"] for r in events[-2]["results"]} == {"web"}


async def test_deep_search_asks_for_more_and_reads_pages():
    pages = []

    async def searxng(query, page):
        pages.append(page)
        return _serp(10, prefix=f"p{page}-")

    read = []

    async def reader(url):
        read.append(url)
        return "Solar panels turn sunlight into power using photovoltaic cells."

    events = await _events(
        SearchRequest(query="solar panels", mode="deep"),
        FakeRunner(),
        reader=reader,
        searxng=searxng,
    )
    phases = [e["phase"] for e in events if e["type"] == "phase"]
    assert phases == ["searching"] + ["reading"] * 6 + ["ranking"]
    progress = [e["progress"] for e in events if e["type"] == "phase"]
    assert progress == sorted(progress)
    results = events[-2]["results"]
    assert len(results) == 25
    assert pages == [1, 2, 3]
    assert len(read) == 5
    assert results[0]["snippet"].startswith("Solar panels turn sunlight")


async def test_platform_search_uses_its_capability():
    output = RedditOutput.model_validate(
        {
            "items": [
                {"dataType": "post", "url": "https://reddit.com/r/a/1", "title": "t"}
            ]
        }
    )
    runner = FakeRunner({"reddit.scrape": output})
    events = await _events(SearchRequest(query="solar", source="reddit"), runner)
    assert runner.calls[0][1].search_queries == ["solar"]
    assert runner.calls[0][1].max_items == 10
    assert events[-2]["results"][0]["source"] == "reddit"


async def test_out_of_credit_is_an_error_event():
    runner = FakeRunner(error=InsufficientCreditsError("no", 0, 100))
    events = await _events(SearchRequest(query="solar", source="amazon"), runner)
    assert events[-1] == {
        "type": "error",
        "code": "out_of_credit",
        "message": OUT_OF_CREDIT,
    }
    assert all(e["type"] != "results" for e in events)


async def test_scraper_failure_is_a_safe_error_event():
    runner = FakeRunner(error=RuntimeError("proxy 10.0.0.3 refused"))
    events = await _events(SearchRequest(query="solar", source="youtube"), runner)
    assert events[-1]["type"] == "error"
    assert events[-1]["code"] == "search_failed"
    assert "10.0.0.3" not in events[-1]["message"]


async def test_a_platform_that_refuses_is_a_blocked_error_event():
    # The scraper raises ForbiddenError when every attempt hit the site's wall;
    # the panel words "source_blocked" itself, no internals leak.
    refused = ForbiddenError(
        "Reddit refused anonymous access: could not mint loid after 8 IP rotations",
        code="REDDIT_ACCESS_BLOCKED",
    )
    runner = FakeRunner(error=refused)
    events = await _events(SearchRequest(query="solar", source="reddit"), runner)
    assert events[-1] == {
        "type": "error",
        "code": "source_blocked",
        "message": "Reddit is blocking automated searches right now.",
    }


@pytest.fixture
def quick_scholar(monkeypatch):
    from app.config import config
    from app.notebooks.research import search as search_module
    from app.notebooks.research.academic.scholar import search as scholar_search

    monkeypatch.setattr(config, "SCHOLAR_MIN_INTERVAL_S", 0.0)
    monkeypatch.setattr(search_module, "KEEPALIVE_S", 0.01)
    scholar_search.clear_cache()


async def test_a_slow_search_keeps_sending_searching_events(quick_scholar):
    import asyncio

    from app.notebooks.research.academic.clients import AcademicClients

    async def slow_page(url):
        await asyncio.sleep(0.08)
        return None  # walled: the search ends in a clear error

    events = [
        e
        async for e in research_events(
            SearchRequest(query="hfnc", source="google_scholar"),
            FakeRunner(),
            _no_read,
            _no_searxng,
            AcademicClients(fetch_scholar_page=slow_page),
        )
    ]
    searching = [e for e in events if e.get("phase") == "searching"]
    assert len(searching) >= 3
    progress = [e["progress"] for e in searching]
    assert progress == sorted(progress) and max(progress) <= 35
    assert events[-1]["code"] == "source_blocked"


async def test_a_disconnected_reader_stops_the_search(quick_scholar):
    import asyncio

    from app.notebooks.research.academic.clients import AcademicClients

    started, stopped = asyncio.Event(), asyncio.Event()

    async def endless_page(url):
        started.set()
        try:
            await asyncio.sleep(60)
        finally:
            stopped.set()

    async def read_stream():
        async for _ in research_events(
            SearchRequest(query="hfnc stop", source="google_scholar"),
            FakeRunner(),
            _no_read,
            _no_searxng,
            AcademicClients(fetch_scholar_page=endless_page),
        ):
            pass

    reader = asyncio.ensure_future(read_stream())
    await asyncio.wait_for(started.wait(), 1)
    reader.cancel()  # what Starlette does when the client goes away
    await asyncio.gather(reader, return_exceptions=True)
    await asyncio.wait_for(stopped.wait(), 1)
