"""``SCRAPER_DIRECT_PLATFORMS``: listed platform scrapers get no proxy URL."""

import pytest

from app.config import Config
from app.utils import proxy
from app.utils.proxy import registry
from app.utils.proxy.direct import goes_direct

pytestmark = pytest.mark.unit

_PROXY = "http://u:p@home.example:8888"


@pytest.fixture(autouse=True)
def _one_proxy(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(Config, "PROXY_PROVIDER", "custom")
    monkeypatch.setattr(Config, "PROXY_URL", _PROXY)
    monkeypatch.setattr(Config, "PROXY_URLS", None)
    monkeypatch.setattr(Config, "SCRAPER_DIRECT_PLATFORMS", frozenset())
    monkeypatch.setattr(registry, "_active_provider", None)


def _direct(monkeypatch: pytest.MonkeyPatch, *platforms: str) -> None:
    monkeypatch.setattr(Config, "SCRAPER_DIRECT_PLATFORMS", frozenset(platforms))


def test_unset_keeps_every_platform_on_the_proxy() -> None:
    assert proxy.get_proxy_url("reddit") == _PROXY
    assert proxy.get_geo_proxy_url("us", platform="tiktok") == _PROXY
    assert proxy.get_sticky_proxy_url("s1", "gb", platform="reddit") == _PROXY
    assert proxy.get_requests_proxies("youtube") == {"http": _PROXY, "https": _PROXY}


def test_listed_platforms_get_no_proxy(monkeypatch: pytest.MonkeyPatch) -> None:
    _direct(monkeypatch, "reddit", "tiktok")

    assert proxy.get_proxy_url("reddit") is None
    assert proxy.get_sticky_proxy_url("s1", "gb", platform="reddit") is None
    assert proxy.get_geo_proxy_url("us", platform="tiktok") is None
    # Not listed: still proxied.
    assert proxy.get_proxy_url("instagram") == _PROXY
    assert proxy.get_requests_proxies("youtube") == {"http": _PROXY, "https": _PROXY}


def test_callers_without_a_platform_are_never_affected(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # The web crawler and captcha solving pass no platform.
    _direct(monkeypatch, "reddit", "tiktok", "youtube")
    assert proxy.get_proxy_url() == _PROXY
    assert proxy.get_geo_proxy_url("us") == _PROXY
    assert proxy.get_requests_proxies() == {"http": _PROXY, "https": _PROXY}


def test_platform_names_match_case_insensitively(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _direct(monkeypatch, "reddit")
    assert goes_direct("Reddit")
    assert not goes_direct(None)
    assert not goes_direct("")
