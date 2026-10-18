"""Instagram URLs are built on instagram.com, whatever the handle looks like.

A handle is the first segment of ``<handle>/embed/``. Handles may start with
``http`` (``http.kitchen``, ``httpster``), so a path is absolute only when it
has a scheme, and a built URL that leaves Instagram is refused. No network.
"""

from __future__ import annotations

from urllib.parse import urlsplit

import pytest

from app.proprietary.platforms.instagram import scraper
from app.proprietary.platforms.instagram.fetch import _build_url, _current_session


@pytest.mark.parametrize(
    ("path", "url"),
    [
        ("http.kitchen/embed/", "https://www.instagram.com/http.kitchen/embed/"),
        ("httpster/", "https://www.instagram.com/httpster/"),
        ("p/ABC123/", "https://www.instagram.com/p/ABC123/"),
        ("https://www.instagram.com/natgeo/", "https://www.instagram.com/natgeo/"),
    ],
)
def test_paths_stay_on_instagram(path, url):
    assert _build_url(path, None) == url


def test_params_are_appended():
    assert (
        _build_url("api/v1/users/web_profile_info/", {"username": "httpster"})
        == "https://www.instagram.com/api/v1/users/web_profile_info/?username=httpster"
    )


@pytest.mark.parametrize(
    "path", ["https://evil.example/embed/", "http://evil.example", "http://10.0.0.1/"]
)
def test_urls_off_instagram_are_refused(path):
    with pytest.raises(ValueError):
        _build_url(path, None)


class _Page:
    status = 404
    cookies: dict = {}
    url = None


class _RecordingSession:
    def __init__(self) -> None:
        self.urls: list[str] = []

    async def get(self, url, headers=None, cookies=None):
        self.urls.append(url)
        return _Page()


class _Holder:
    def __init__(self, session: _RecordingSession) -> None:
        self.session = session
        self.warmed = True
        self.rotations = 0

    async def pace(self) -> None:
        return None

    async def rotate(self):
        raise AssertionError("a 404 must not rotate")


async def test_handle_starting_with_http_is_read_from_instagram():
    session = _RecordingSession()
    token = _current_session.set(_Holder(session))
    try:
        assert await scraper._profile_user("http.kitchen") is None
    finally:
        _current_session.reset(token)

    assert "https://www.instagram.com/http.kitchen/embed/" in session.urls
    assert "https://www.instagram.com/http.kitchen/" in session.urls
    assert {urlsplit(u).hostname for u in session.urls} == {"www.instagram.com"}


@pytest.mark.parametrize("name", ["evil.example/x", "a b", "x?y=1", "a" * 31])
async def test_a_name_that_is_not_a_handle_is_never_fetched(monkeypatch, name):
    async def _no_fetch(path, params=None):
        raise AssertionError(f"fetched {path!r}")

    monkeypatch.setattr(scraper, "fetch_html", _no_fetch)

    assert await scraper._profile_user(name) is None
