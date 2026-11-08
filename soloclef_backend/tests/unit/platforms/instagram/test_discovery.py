"""Offline tests for Google-backed Instagram discovery.

Hashtag/place feeds are login-walled. A query shaped like a handle is read as
that account when it exists; any other query, and a one-word query with no such
account, goes to the ``google_search`` platform (``site:instagram.com``),
classifying organic results with ``resolve_url`` and keeping profile hits (plus
post/reel hits when the run wants posts or reels). These tests inject a fake
``scrape_serps`` and profile read so there is no network: they pin the
classification, de-dup, ``limit`` cap and the account-or-keyword choice.
"""

from __future__ import annotations

from contextlib import asynccontextmanager

import pytest

from app.proprietary.platforms.instagram import scraper
from app.proprietary.platforms.instagram.fetch import InstagramAccessBlockedError


@pytest.fixture(autouse=True)
def _no_sessions(monkeypatch):
    """Proxy sessions and holders are no-ops: nothing here touches the network."""

    @asynccontextmanager
    async def _session():
        yield None

    async def _open():
        return None

    monkeypatch.setattr(scraper, "proxy_session", _session)
    monkeypatch.setattr(scraper, "open_proxy_holder", _open)


def _accounts(monkeypatch, known: dict[str, dict], walled: set[str] = frozenset()):
    """Stub the profile read: ``known`` exist, ``walled`` are blocked; returns reads."""
    reads: list[str] = []

    async def _profile_user(username):
        reads.append(username)
        if username in walled:
            raise InstagramAccessBlockedError("login wall")
        return known.get(username)

    monkeypatch.setattr(scraper, "_profile_user", _profile_user)
    return reads


def _fake_serps(*organic_urls: str):
    async def _scrape_serps(input_model, *, limit=None):
        assert input_model.site == "instagram.com"
        return [{"organicResults": [{"url": u} for u in organic_urls]}]

    return _scrape_serps


async def test_google_discovery_keeps_only_profiles(monkeypatch):
    # A non-handle query goes to Google; only profile URLs survive (hashtag /
    # post / non-instagram results are dropped since discovery is profile-only).
    monkeypatch.setattr(
        scraper,
        "scrape_serps",
        _fake_serps(
            "https://www.instagram.com/natgeo/",
            "https://www.instagram.com/explore/tags/travel/",
            "https://www.instagram.com/p/ABC123/",
            "https://example.com/not-instagram",
        ),
    )
    targets = await scraper._discover("nat geo photos", search_type="profile", limit=10)
    assert [(t.kind, t.value) for t in targets] == [("profile", "natgeo")]


async def test_google_discovery_dedupes(monkeypatch):
    monkeypatch.setattr(
        scraper,
        "scrape_serps",
        _fake_serps(
            "https://www.instagram.com/natgeo/",
            "https://www.instagram.com/natgeo/",
        ),
    )
    targets = await scraper._discover("nat geo photos", search_type="profile", limit=10)
    assert len(targets) == 1


async def test_google_discovery_respects_limit(monkeypatch):
    monkeypatch.setattr(
        scraper,
        "scrape_serps",
        _fake_serps(
            "https://www.instagram.com/a_a/",
            "https://www.instagram.com/b_b/",
            "https://www.instagram.com/c_c/",
        ),
    )
    targets = await scraper._discover("some brand name", search_type="profile", limit=2)
    assert [t.value for t in targets] == ["a_a", "b_b"]


async def test_discover_profile_handle_fast_path_skips_google(monkeypatch):
    # An existing account resolves directly without touching Google, and the
    # profile read is kept for the flow.
    async def _boom(input_model, *, limit=None):
        raise AssertionError("Google should not be called for a valid handle")

    monkeypatch.setattr(scraper, "scrape_serps", _boom)
    _accounts(monkeypatch, {"messi": {"username": "messi"}})
    profiles: dict = {}
    targets = await scraper._discover(
        "@messi", search_type="user", limit=10, profiles=profiles
    )
    assert [(t.kind, t.value) for t in targets] == [("profile", "messi")]
    assert profiles == {"messi": {"username": "messi"}}


async def test_one_word_that_is_not_an_account_is_searched_on_google(monkeypatch):
    # Regression: a one-word keyword was always taken as a handle, so a word
    # with no such account found nothing (or reported "blocked").
    monkeypatch.setattr(
        scraper, "scrape_serps", _fake_serps("https://www.instagram.com/p/ABC123/")
    )
    _accounts(monkeypatch, {})
    targets = await scraper._discover(
        "zzqxsourdoughzz", search_type="user", limit=10, with_media=True
    )
    assert [(t.kind, t.value) for t in targets] == [("post", "ABC123")]


async def test_walled_account_lookup_raises(monkeypatch):
    _accounts(monkeypatch, {}, walled={"natgeo"})
    with pytest.raises(InstagramAccessBlockedError):
        await scraper._discover("natgeo", search_type="user", limit=10)


async def test_one_walled_query_keeps_the_others(monkeypatch):
    _accounts(monkeypatch, {"nasa": {"username": "nasa"}}, walled={"natgeo"})
    model = scraper.InstagramScrapeInput(resultsType="posts", search="natgeo, nasa")
    targets = await scraper._targets(model, {})
    assert [t.value for t in targets] == ["nasa"]


async def test_every_query_walled_raises(monkeypatch):
    _accounts(monkeypatch, {}, walled={"natgeo", "nasa"})
    model = scraper.InstagramScrapeInput(resultsType="posts", search="natgeo, nasa")
    with pytest.raises(InstagramAccessBlockedError):
        await scraper._targets(model, {})


async def test_missing_account_keyword_run_returns_google_posts(monkeypatch):
    # The reviewer's research-box case end to end: a one-word keyword that is
    # not an account returns the posts Google finds, not a "blocked" error.
    monkeypatch.setattr(
        scraper, "scrape_serps", _fake_serps("https://www.instagram.com/p/ABC123/")
    )
    _accounts(monkeypatch, {})

    async def _fetch_html(path, params=None):
        assert path == "p/ABC123/"
        return "<html>post</html>"

    def _parse_post(html, *, url, shortcode):
        return {"id": "1", "shortCode": shortcode, "url": url}

    monkeypatch.setattr(scraper, "fetch_html", _fetch_html)
    monkeypatch.setattr(scraper, "parse_post", _parse_post)
    model = scraper.InstagramScrapeInput(resultsType="posts", search="zzqxsourdoughzz")

    items = [item async for item in scraper.iter_instagram(model)]

    assert [i["shortCode"] for i in items] == ["ABC123"]


async def test_account_found_in_discovery_is_read_once(monkeypatch):
    user = {
        "username": "natgeo",
        "edge_owner_to_timeline_media": {"edges": [{"node": {"id": "7"}}]},
    }
    reads = _accounts(monkeypatch, {"natgeo": user})
    model = scraper.InstagramScrapeInput(resultsType="posts", search="natgeo")

    items = [item async for item in scraper.iter_instagram(model)]

    assert [i["id"] for i in items] == ["7"]
    assert reads == ["natgeo"]


async def test_google_discovery_keeps_posts_and_reels_when_media_is_wanted(monkeypatch):
    # Google's site:instagram.com results for a topic are mostly posts and reels;
    # a posts/reels run keeps them (single post pages read anonymously).
    monkeypatch.setattr(
        scraper,
        "scrape_serps",
        _fake_serps(
            "https://www.instagram.com/reel/C8UYK8FOt9E/?hl=bg",
            "https://www.instagram.com/explore/tags/travel/",
            "https://www.instagram.com/p/ABC123/",
            "https://www.instagram.com/natgeo/",
        ),
    )
    targets = await scraper._discover(
        "sourdough bakery", search_type="profile", limit=10, with_media=True
    )
    assert [(t.kind, t.value) for t in targets] == [
        ("reel", "C8UYK8FOt9E"),
        ("post", "ABC123"),
        ("profile", "natgeo"),
    ]


async def test_search_run_for_posts_asks_for_media_but_details_do_not(monkeypatch):
    seen: list[bool] = []

    async def _fake_discover(query, *, search_type, limit, with_media=False, **_):
        seen.append(with_media)
        return []

    monkeypatch.setattr(scraper, "_discover", _fake_discover)
    for results_type in ("posts", "reels", "details"):
        model = scraper.InstagramScrapeInput(
            resultsType=results_type, search="sourdough bakery"
        )
        await scraper._targets(model)
    assert seen == [True, True, False]
