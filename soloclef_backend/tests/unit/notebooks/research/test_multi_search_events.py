"""A search of several sources runs them side by side and merges what they find."""

from __future__ import annotations

import asyncio

import pytest
from pydantic import ValidationError

from app.capabilities.reddit.scrape.schemas import ScrapeOutput as RedditOutput
from app.capabilities.youtube.scrape.schemas import ScrapeOutput as YoutubeOutput
from app.notebooks.research import multi_search
from app.notebooks.research.multi_search import interleave, multi_research_events
from app.notebooks.research.schemas import ResultRow, SearchRequest
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


def _reddit(*urls: str) -> RedditOutput:
    return RedditOutput.model_validate(
        {"items": [{"dataType": "post", "url": u, "title": u} for u in urls]}
    )


class FakeRunner:
    """One source's runner: its own outputs, its own calls, its own error."""

    def __init__(self, outputs=None, error=None, delay=0.0):
        self.outputs = outputs or {}
        self.error = error
        self.delay = delay
        self.calls = []

    async def run(self, capability, payload):
        self.calls.append((capability.name, payload))
        if self.delay:
            await asyncio.sleep(self.delay)
        if self.error:
            raise self.error
        return self.outputs[capability.name]


async def _searxng(query, page):
    return _serp(12, prefix="web")


async def _no_read(url):
    return None


def _request(*sources, mode="fast"):
    return SearchRequest(query="solar", sources=list(sources), mode=mode)


async def _events(request, runners, reader_for=None, searxng=_searxng):
    return [
        e
        async for e in multi_research_events(
            request,
            runners,
            reader_for or (lambda source: _no_read),
            searxng,
        )
    ]


def _providers(events):
    return [(e["source"], e["status"]) for e in events if e["type"] == "provider"]


# --- the request ---------------------------------------------------------------


def test_sources_set_the_primary_source_and_collapse_repeats():
    request = SearchRequest(query="q", sources=["reddit", "web", "reddit"])
    assert request.source == "reddit"
    assert request.selected == ["reddit", "web"]
    assert SearchRequest(query="q", source="arxiv").selected == ["arxiv"]


def test_at_most_four_sources_and_only_known_ones():
    with pytest.raises(ValidationError):
        SearchRequest(
            query="q", sources=["web", "reddit", "youtube", "arxiv", "pubmed"]
        )
    with pytest.raises(ValidationError):
        SearchRequest(query="q", sources=["web", "bing"])
    with pytest.raises(ValidationError):
        SearchRequest(query="q", sources=[])


# --- merging ------------------------------------------------------------------


def _row(source, n, url=None):
    url = url or f"https://{source}.example/{n}"
    return ResultRow(id=url, url=url, title=f"{source}{n}", snippet="", source=source)


def test_interleave_takes_turns_and_keeps_the_first_copy_of_a_page():
    shared = "https://both.example/page"
    web = [_row("web", 1), _row("web", 2, shared), _row("web", 3)]
    reddit = [_row("reddit", 1, shared), _row("reddit", 2)]
    merged = interleave([web, reddit])
    assert [(r.source, r.url) for r in merged] == [
        ("web", "https://web.example/1"),
        ("reddit", shared),
        ("reddit", "https://reddit.example/2"),
        ("web", "https://web.example/3"),
    ]


# --- the event stream ------------------------------------------------------------


async def test_every_source_reports_then_one_merged_list_arrives():
    reddit = FakeRunner({"reddit.scrape": _reddit("https://reddit.com/r/a/1")})
    web = FakeRunner()
    events = await _events(_request("web", "reddit"), {"web": web, "reddit": reddit})

    assert events[0] == {"type": "phase", "phase": "searching", "progress": 5}
    assert _providers(events)[:2] == [("web", "searching"), ("reddit", "searching")]
    finished = {
        e["source"]: e for e in events if e["type"] == "provider" and "count" in e
    }
    assert finished["web"]["count"] == 10
    assert finished["reddit"]["count"] == 1
    assert [e["type"] for e in events][-2:] == ["results", "done"]
    results = events[-2]["results"]
    assert [r["source"] for r in results[:3]] == ["web", "reddit", "web"]
    assert len(results) == 11
    # Each source ran on its own runner: its own bill.
    assert web.calls == []  # SearXNG answered, nothing billed
    assert [name for name, _ in reddit.calls] == ["reddit.scrape"]


async def test_the_sources_run_side_by_side():
    started = []

    class SlowRunner(FakeRunner):
        async def run(self, capability, payload):
            started.append(capability.name)
            return await super().run(capability, payload)

    reddit = SlowRunner(
        {"reddit.scrape": _reddit("https://reddit.com/r/a/1")}, delay=0.2
    )
    youtube = SlowRunner({"youtube.scrape": YoutubeOutput(items=[])}, delay=0.2)
    loop = asyncio.get_running_loop()
    began = loop.time()
    await _events(_request("reddit", "youtube"), {"reddit": reddit, "youtube": youtube})
    assert loop.time() - began < 0.35
    assert sorted(started) == ["reddit.scrape", "youtube.scrape"]


async def test_one_failing_source_is_reported_and_the_others_still_answer():
    reddit = FakeRunner(error=RuntimeError("proxy 10.0.0.3 refused"))
    events = await _events(
        _request("web", "reddit"), {"web": FakeRunner(), "reddit": reddit}
    )
    [failed] = [e for e in events if e["type"] == "provider" and e["status"] == "error"]
    assert failed["source"] == "reddit"
    assert failed["code"] == "search_failed"
    assert "10.0.0.3" not in failed["message"]
    assert events[-1] == {"type": "done"}
    assert {r["source"] for r in events[-2]["results"]} == {"web"}


async def test_out_of_credit_is_reported_per_source():
    reddit = FakeRunner(error=InsufficientCreditsError("no", 0, 100))
    events = await _events(
        _request("web", "reddit"), {"web": FakeRunner(), "reddit": reddit}
    )
    [failed] = [e for e in events if e.get("status") == "error"]
    assert (failed["source"], failed["code"]) == ("reddit", "out_of_credit")


async def test_a_source_past_its_time_limit_is_dropped(monkeypatch):
    monkeypatch.setattr(multi_search, "SOURCE_TIMEOUT_S", 0.05)
    reddit = FakeRunner({"reddit.scrape": _reddit()}, delay=5)
    events = await _events(
        _request("web", "reddit"), {"web": FakeRunner(), "reddit": reddit}
    )
    [late] = [e for e in events if e.get("status") == "error"]
    assert (late["source"], late["code"]) == ("reddit", "source_timeout")
    assert events[-1] == {"type": "done"}


async def test_when_every_source_fails_the_search_fails_after_the_first_pick():
    events = await _events(
        _request("youtube", "reddit"),
        {
            "youtube": FakeRunner(error=InsufficientCreditsError("no", 0, 1)),
            "reddit": FakeRunner(error=RuntimeError("down")),
        },
    )
    assert events[-1] == {
        "type": "error",
        "code": "out_of_credit",
        "message": multi_search.OUT_OF_CREDIT,
        "source": "youtube",
    }
    assert all(e["type"] != "results" for e in events)


async def test_deep_reads_the_top_pages_on_the_bill_of_the_source_that_found_them():
    read_by = []

    def reader_for(source):
        async def read(url):
            read_by.append((source, url))
            return "Solar panels turn sunlight into power using photovoltaic cells."

        return read

    events = await _events(
        _request("web", "reddit", mode="deep"),
        {
            "web": FakeRunner(),
            "reddit": FakeRunner({"reddit.scrape": _reddit("https://reddit.com/r/1")}),
        },
        reader_for=reader_for,
    )
    phases = [e["phase"] for e in events if e["type"] == "phase"]
    assert "reading" in phases
    progress = [e["progress"] for e in events if e["type"] == "phase"]
    assert progress == sorted(progress)
    # Only ordinary web pages are read; platform posts read poorly.
    assert {source for source, _ in read_by} == {"web"}
    assert len(read_by) == 5
    assert events[-2]["results"][0]["snippet"].startswith("Solar panels")


async def test_a_disconnected_reader_stops_every_source():
    started, stopped = asyncio.Event(), asyncio.Event()

    class EndlessRunner:
        async def run(self, capability, payload):
            started.set()
            try:
                await asyncio.sleep(60)
            finally:
                stopped.set()

    async def read_stream():
        async for _ in multi_research_events(
            _request("reddit", "youtube"),
            {"reddit": EndlessRunner(), "youtube": EndlessRunner()},
            lambda source: _no_read,
            _searxng,
        ):
            pass

    reader = asyncio.ensure_future(read_stream())
    await asyncio.wait_for(started.wait(), 1)
    reader.cancel()
    await asyncio.gather(reader, return_exceptions=True)
    await asyncio.wait_for(stopped.wait(), 1)
