"""Reddit without a proxy, and a walled run that must say so (no network).

Checked 2026-10-09: Reddit's network-security wall refused the home proxy's
exit while the server's own address passed the browser warm. So a direct
session must still warm and fetch, and a run where every target hit the wall
must raise (-> "Reddit is blocking") instead of returning an empty success.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import pytest

from app.proprietary.platforms.reddit import fetch, scraper
from app.proprietary.platforms.reddit.fetch import (
    RedditAccessBlockedError,
    _current_session,
    fetch_json,
)
from app.proprietary.platforms.reddit.schemas import RedditScrapeInput

_LISTING = {"kind": "Listing", "data": {"children": [], "after": None}}


class _Page:
    status = 200

    def json(self):
        return _LISTING

    @property
    def body(self) -> str:
        return json.dumps(_LISTING)


class _Session:
    async def get(self, url, headers=None, cookies=None):
        return _Page()


def _direct_session(monkeypatch, *, warm_jar: dict | None) -> tuple[list, list]:
    """No proxy for Reddit; a fake HTTP session and a fake browser warm."""
    opened: list[dict] = []
    warmed_with: list[str | None] = []

    class _SessionCM:
        def __init__(self, **kwargs):
            opened.append(kwargs)

        async def __aenter__(self):
            return _Session()

        async def __aexit__(self, *exc):
            return None

    async def _warm(proxy):
        warmed_with.append(proxy)
        return warm_jar

    monkeypatch.setattr(fetch, "get_sticky_proxy_url", lambda *a, **k: None)
    monkeypatch.setattr(fetch, "FetcherSession", _SessionCM)
    monkeypatch.setattr(fetch, "warm_session", _warm)
    return opened, warmed_with


async def test_direct_session_warms_and_fetches(monkeypatch):
    opened, warmed_with = _direct_session(monkeypatch, warm_jar={"loid": "x"})

    holder = await fetch.open_proxy_holder()
    token = _current_session.set(holder)
    try:
        result = await fetch_json("r/python/hot")
    finally:
        _current_session.reset(token)
        await holder.close()

    assert result == _LISTING
    assert opened[0]["proxy"] is None  # a real session, just not proxied
    assert warmed_with == [None]  # the browser warm ran from the server
    assert holder.max_rotations == fetch._DIRECT_MAX_ROTATIONS


async def test_direct_wall_gives_up_after_one_fresh_session(monkeypatch):
    _opened, warmed_with = _direct_session(monkeypatch, warm_jar=None)

    holder = await fetch.open_proxy_holder()
    token = _current_session.set(holder)
    try:
        with pytest.raises(RedditAccessBlockedError):
            await fetch_json("r/python/hot")
    finally:
        _current_session.reset(token)
        await holder.close()

    # No other exit IP exists: one retry, not the proxied budget of 8.
    assert holder.rotations == fetch._DIRECT_MAX_ROTATIONS
    assert len(warmed_with) == fetch._DIRECT_MAX_ROTATIONS + 1


def _fake_holders(monkeypatch) -> None:
    class _Holder:
        async def close(self) -> None:
            return None

    async def _open():
        return _Holder()

    @asynccontextmanager
    async def _bind(holder):
        yield holder

    monkeypatch.setattr(scraper, "open_proxy_holder", _open)
    monkeypatch.setattr(scraper, "bind_proxy_holder", _bind)


async def _blocked_job() -> AsyncIterator[dict]:
    raise RedditAccessBlockedError("network security wall")
    yield {}  # unreachable; makes this an async generator


async def _good_job() -> AsyncIterator[dict]:
    for i in range(2):
        yield {"id": f"t3_{i}"}


async def test_fan_out_all_blocked_raises(monkeypatch):
    _fake_holders(monkeypatch)
    async with asyncio.timeout(5):
        with pytest.raises(RedditAccessBlockedError):
            async for _ in scraper.fan_out([_blocked_job(), _blocked_job()]):
                pass


async def test_fan_out_partial_results_survive_a_blocked_target(monkeypatch):
    _fake_holders(monkeypatch)
    async with asyncio.timeout(5):
        items = [i async for i in scraper.fan_out([_blocked_job(), _good_job()])]
    assert [i["id"] for i in items] == ["t3_0", "t3_1"]


async def test_walled_comment_threads_keep_the_posts(monkeypatch):
    async def _listing(path, params, kinds, **kwargs):
        yield {"name": "t3_abc", "id": "abc", "title": "A post", "subreddit": "python"}

    async def _walled_fan_out(jobs, *, concurrency=8):
        for job in jobs:
            await job.aclose()
        raise RedditAccessBlockedError("comments walled")
        yield {}  # unreachable; makes this an async generator

    monkeypatch.setattr(scraper, "_paginate_listing", _listing)
    monkeypatch.setattr(scraper, "fan_out", _walled_fan_out)

    model = RedditScrapeInput(skipCommunity=True, skipComments=False)
    items = [i async for i in scraper._subreddit_flow("python", input_model=model)]

    assert [i["id"] for i in items] == ["t3_abc"]
