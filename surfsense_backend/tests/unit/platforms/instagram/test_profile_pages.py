"""Profiles read from Instagram's public pages (no network).

The anonymous ``web_profile_info`` endpoint answers 401 ``require_login`` now
(checked 2026-10-09), so a profile comes from ``/<username>/embed/`` (counts and
the latest posts) and ``/<username>/`` (biography, following, links). The
fixtures are trimmed copies of the real natgeo pages from that day.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from app.proprietary.platforms.instagram import scraper
from app.proprietary.platforms.instagram.fetch import InstagramAccessBlockedError
from app.proprietary.platforms.instagram.parsers import (
    merge_profile_users,
    parse_profile,
    parse_profile_embed,
    parse_profile_page,
)

_FIXTURES = Path(__file__).parent / "fixtures"
EMBED_HTML = (_FIXTURES / "profile_embed.html").read_text()
PAGE_HTML = (_FIXTURES / "profile_page.html").read_text()


def test_embed_gives_counts_and_latest_posts():
    user = parse_profile_embed(EMBED_HTML)

    assert user is not None
    assert user["id"] == "787132"
    assert user["username"] == "natgeo"
    assert user["full_name"] == "National Geographic"
    assert user["is_verified"] is True
    assert user["edge_followed_by"]["count"] == 268420490
    media = user["edge_owner_to_timeline_media"]
    assert media["count"] == 32057
    assert [e["node"]["shortcode"] for e in media["edges"]] == [
        "DeQmgjNCPw2",
        "DePpLegAuKI",
    ]


def test_page_gives_biography_following_and_link():
    user = parse_profile_page(PAGE_HTML, "NatGeo")

    assert user is not None
    assert user["id"] == "787132"  # the pk, not the node's business id
    assert user["biography"].startswith("Step into wonder")
    assert user["external_url"] == "http://visitstore.bio/natgeo"
    assert user["edge_follow"]["count"] == 194
    assert user["is_private"] is False


def test_page_for_another_handle_is_ignored():
    assert parse_profile_page(PAGE_HTML, "someoneelse") is None


@pytest.mark.parametrize("html", [None, "", "<html>login</html>"])
def test_pages_without_data_parse_to_none(html):
    assert parse_profile_embed(html) is None
    assert parse_profile_page(html, "natgeo") is None


def test_broken_context_json_parses_to_none():
    assert parse_profile_embed('<script>{"contextJSON":"{not json"}</script>') is None


def test_merged_profile_reads_like_web_profile_info():
    user = merge_profile_users(
        parse_profile_embed(EMBED_HTML), parse_profile_page(PAGE_HTML, "natgeo")
    )
    item = parse_profile(user)

    assert item["username"] == "natgeo"
    assert item["fullName"] == "National Geographic"
    assert item["biography"].startswith("Step into wonder")
    assert item["externalUrl"] == "http://visitstore.bio/natgeo"
    assert item["followersCount"] == 268420490  # the embed's count wins
    assert item["followsCount"] == 194
    assert item["postsCount"] == 32057
    assert item["verified"] is True
    assert item["private"] is False
    latest = item["latestPosts"]
    assert [p["shortCode"] for p in latest] == ["DeQmgjNCPw2", "DePpLegAuKI"]
    assert latest[0]["type"] == "Video"
    assert latest[0]["url"] == "https://www.instagram.com/p/DeQmgjNCPw2/"
    assert latest[0]["likesCount"] and latest[0]["timestamp"]
    assert latest[0]["ownerUsername"] == "natgeo"


def test_merge_keeps_whichever_page_parsed():
    page = parse_profile_page(PAGE_HTML, "natgeo")
    embed = parse_profile_embed(EMBED_HTML)
    assert merge_profile_users(None, page) is page
    assert merge_profile_users(embed, None) is embed
    assert merge_profile_users(None, None) is None


def _pages(monkeypatch, pages: dict[str, str | Exception | None]) -> list[str]:
    fetched: list[str] = []

    async def _fetch_html(path, params=None):
        fetched.append(path)
        page = pages.get(path)
        if isinstance(page, Exception):
            raise page
        return page

    monkeypatch.setattr(scraper, "fetch_html", _fetch_html)
    return fetched


async def test_profile_comes_from_the_pages(monkeypatch):
    fetched = _pages(monkeypatch, {"natgeo/embed/": EMBED_HTML, "natgeo/": PAGE_HTML})

    user = await scraper._profile_user("natgeo")

    assert user["biography"].startswith("Step into wonder")
    assert len(user["edge_owner_to_timeline_media"]["edges"]) == 2
    assert fetched == ["natgeo/embed/", "natgeo/"]


async def test_walled_profile_page_still_gives_the_embed(monkeypatch):
    walled = InstagramAccessBlockedError("login wall")
    _pages(monkeypatch, {"natgeo/embed/": EMBED_HTML, "natgeo/": walled})

    user = await scraper._profile_user("natgeo")

    assert user["username"] == "natgeo"
    assert "biography" not in user


async def test_without_the_embed_the_profile_page_answers(monkeypatch):
    _pages(monkeypatch, {"natgeo/": PAGE_HTML})

    user = await scraper._profile_user("natgeo")

    assert user["biography"].startswith("Step into wonder")


@pytest.mark.parametrize(
    "pages",
    [
        {},  # both pages 404
        {"nobody/embed/": "<html>no data</html>", "nobody/": "<html></html>"},
        {"nobody/embed/": InstagramAccessBlockedError("wall")},  # one answered
        {"nobody/": InstagramAccessBlockedError("wall")},
    ],
)
async def test_pages_without_a_profile_mean_no_such_account(monkeypatch, pages):
    # Regression: this used to fall back to web_profile_info, which answers 401
    # require_login, and so reported a missing account as "blocked".
    fetched = _pages(monkeypatch, pages)

    assert await scraper._profile_user("nobody") is None
    assert fetched == ["nobody/embed/", "nobody/"]


async def test_both_pages_walled_raises(monkeypatch):
    walled = InstagramAccessBlockedError("login wall")
    _pages(monkeypatch, {"natgeo/embed/": walled, "natgeo/": walled})

    with pytest.raises(InstagramAccessBlockedError):
        await scraper._profile_user("natgeo")
