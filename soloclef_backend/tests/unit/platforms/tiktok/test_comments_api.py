"""Comments over TikTok's plain web API, browser as the fallback (no network)."""

from __future__ import annotations

from typing import Any
from urllib.parse import parse_qs, urlsplit

from app.proprietary.platforms.tiktok.session import client, comments_api
from app.proprietary.platforms.tiktok.session.proxy import _current_session

_VIDEO = "https://www.tiktok.com/@tiktok/video/7694307066965396766"


def _comments(start: int, n: int) -> list[dict[str, Any]]:
    return [{"cid": str(i), "text": f"c{i}"} for i in range(start, start + n)]


def _api(pages: list[Any]):
    """A fake ``fetch_json`` answering successive pages; records each query."""
    asked: list[dict[str, str]] = []

    async def fetch(url: str):
        asked.append({k: v[0] for k, v in parse_qs(urlsplit(url).query).items()})
        return pages[len(asked) - 1] if len(asked) <= len(pages) else None

    return fetch, asked


def test_video_id_from_urls():
    assert comments_api.video_id(_VIDEO) == "7694307066965396766"
    assert comments_api.video_id("https://www.tiktok.com/@a/photo/123") == "123"
    assert comments_api.video_id("https://www.tiktok.com/@a") is None


async def test_pages_until_the_cap():
    fetch, asked = _api(
        [
            {
                "status_code": 0,
                "comments": _comments(0, 50),
                "has_more": 1,
                "cursor": 50,
            },
            {
                "status_code": 0,
                "comments": _comments(50, 20),
                "has_more": 1,
                "cursor": 70,
            },
        ]
    )
    got = await comments_api.fetch_comment_pages(_VIDEO, 70, fetch=fetch)

    assert len(got) == 70
    assert [q["cursor"] for q in asked] == ["0", "50"]
    assert [q["count"] for q in asked] == ["50", "20"]
    assert asked[0]["aweme_id"] == "7694307066965396766"
    assert asked[0]["aid"] == "1988"


async def test_stops_when_there_is_no_more():
    fetch, asked = _api(
        [{"status_code": 0, "comments": _comments(0, 3), "has_more": 0, "cursor": 3}]
    )
    got = await comments_api.fetch_comment_pages(_VIDEO, 40, fetch=fetch)
    assert len(got) == 3
    assert len(asked) == 1


async def test_stops_when_the_cursor_does_not_move():
    page = {"status_code": 0, "comments": _comments(0, 5), "has_more": 1, "cursor": 0}
    fetch, asked = _api([page, page])
    got = await comments_api.fetch_comment_pages(_VIDEO, 40, fetch=fetch)
    assert len(got) == 5
    assert len(asked) == 1


async def test_a_video_without_comments_is_an_answer_not_a_failure():
    fetch, _ = _api([{"status_code": 0, "comments": [], "has_more": 0, "cursor": 0}])
    assert await comments_api.fetch_comment_pages(_VIDEO, 10, fetch=fetch) == []


async def test_no_answer_is_none():
    fetch, _ = _api([None])
    assert await comments_api.fetch_comment_pages(_VIDEO, 10, fetch=fetch) is None
    fetch, _ = _api([{"status_code": 10201, "status_msg": "blocked"}])
    assert await comments_api.fetch_comment_pages(_VIDEO, 10, fetch=fetch) is None


async def test_a_failed_later_page_keeps_the_earlier_ones():
    fetch, _ = _api(
        [
            {
                "status_code": 0,
                "comments": _comments(0, 50),
                "has_more": 1,
                "cursor": 50,
            },
            None,
        ]
    )
    got = await comments_api.fetch_comment_pages(_VIDEO, 80, fetch=fetch)
    assert len(got) == 50


async def test_browser_runs_only_when_the_api_gave_nothing():
    browser_calls: list[str] = []

    async def in_browser(url: str, n: int):
        browser_calls.append(url)
        return _comments(0, 2)

    answered, _ = _api([{"status_code": 0, "comments": [], "has_more": 0}])
    assert (
        await comments_api.fetch_comments(
            _VIDEO, 5, fetch=answered, in_browser=in_browser
        )
        == []
    )
    assert browser_calls == []

    silent, _ = _api([None])
    got = await comments_api.fetch_comments(
        _VIDEO, 5, fetch=silent, in_browser=in_browser
    )
    assert len(got) == 2
    assert browser_calls == [_VIDEO]


class _Page:
    def __init__(self, status: int, text: str, cookies: dict | None = None):
        self.status = status
        self.text = text
        self.cookies = cookies or {}


class _Session:
    def __init__(self, body: str):
        self.body = body

    async def get(self, url, headers=None, cookies=None):
        if url.rstrip("/") == "https://www.tiktok.com":
            return _Page(200, "<html></html>", {"ttwid": "x"})
        return _Page(200, self.body)


class _Holder:
    def __init__(self, body: str):
        self.session = _Session(body)
        self.rotations = 0
        self.warmed = False

    async def rotate(self):
        self.rotations += 1
        return self.session

    async def pace(self) -> None:
        return None


async def _client_json(body: str):
    token = _current_session.set(_Holder(body))
    try:
        return await client.fetch_json(comments_api.page_url("1", 5, 0))
    finally:
        _current_session.reset(token)


async def test_client_parses_api_json():
    assert await _client_json('{"status_code": 0, "comments": []}') == {
        "status_code": 0,
        "comments": [],
    }


async def test_client_soft_block_body_is_none():
    # TikTok soft-blocks with an empty (or non-JSON) 200.
    assert await _client_json("") is None
    assert await _client_json("<html>blocked</html>") is None
