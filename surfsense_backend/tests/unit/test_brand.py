"""The backend brand defaults must equal the web's brand.json, and carry no
server's address: the site URL comes from this server's own configuration."""

from __future__ import annotations

import importlib
import json
from pathlib import Path

import pytest

import app.brand as brand

pytestmark = pytest.mark.unit

BRAND_JSON = (
    Path(__file__).resolve().parents[3]
    / "surfsense_web"
    / "lib"
    / "brand"
    / "brand.json"
)
SITE_ENV = ("BRAND_SITE_URL", "NEXT_FRONTEND_URL", "SURFSENSE_PUBLIC_URL")


@pytest.fixture(scope="module")
def web_brand() -> dict:
    if not BRAND_JSON.exists():
        pytest.skip("surfsense_web/lib/brand/brand.json is not in this checkout")
    return json.loads(BRAND_JSON.read_text())


@pytest.fixture
def reload_brand(monkeypatch: pytest.MonkeyPatch):
    """Re-import app.brand under a given environment, then restore it."""

    def _reload(**env: str):
        for name in (*SITE_ENV, "BRAND_NAME"):
            monkeypatch.delenv(name, raising=False)
        for name, value in env.items():
            monkeypatch.setenv(name, value)
        return importlib.reload(brand)

    yield _reload
    monkeypatch.undo()
    importlib.reload(brand)


def test_defaults_match_web_brand(web_brand: dict) -> None:
    assert web_brand["name"] == brand.DEFAULT_BRAND_NAME
    assert web_brand["shortName"] == brand.BRAND_SHORT_NAME
    assert web_brand["tagline"] == brand.BRAND_TAGLINE
    assert web_brand["description"] == brand.BRAND_DESCRIPTION
    assert web_brand["siteUrl"] == brand.DEFAULT_SITE_URL
    assert web_brand["contactEmail"] == brand.BRAND_CONTACT_EMAIL
    assert web_brand["colors"] == brand.BRAND_COLORS
    assert web_brand["logo"] == brand.BRAND_LOGO


def test_no_server_address_is_a_default() -> None:
    # A new server must not present itself as somebody else's site.
    assert brand.DEFAULT_SITE_URL == ""
    assert brand.BRAND_CONTACT_EMAIL == ""
    assert brand.site_url_from_env({}) == ""


def test_an_unconfigured_server_links_within_itself(reload_brand) -> None:
    reloaded = reload_brand()
    assert reloaded.BRAND_SITE_URL == ""
    assert reloaded.BRAND_DOCS_URL == "/docs"
    assert reloaded.BRAND_CONTACT_URL == "/contact"


def test_the_site_url_is_the_servers_own_address() -> None:
    public = {"SURFSENSE_PUBLIC_URL": "https://notes.example.org/"}
    assert brand.site_url_from_env(public) == "https://notes.example.org"
    frontend = {**public, "NEXT_FRONTEND_URL": "https://app.example.org"}
    assert brand.site_url_from_env(frontend) == "https://app.example.org"
    site = {**frontend, "BRAND_SITE_URL": " https://www.example.org// "}
    assert brand.site_url_from_env(site) == "https://www.example.org"
    blank = {"BRAND_SITE_URL": "   ", "SURFSENSE_PUBLIC_URL": "https://a.example"}
    assert brand.site_url_from_env(blank) == "https://a.example"


def test_an_instance_with_only_its_public_url_keeps_its_links(reload_brand) -> None:
    # docker-compose.yml hands the backend NEXT_FRONTEND_URL from
    # SURFSENSE_PUBLIC_URL; either alone gives the same links.
    for env in (
        {"SURFSENSE_PUBLIC_URL": "https://notes.example.org"},
        {
            "SURFSENSE_PUBLIC_URL": "https://notes.example.org",
            "NEXT_FRONTEND_URL": "https://notes.example.org",
        },
    ):
        reloaded = reload_brand(**env)
        assert reloaded.BRAND_SITE_URL == "https://notes.example.org"
        assert reloaded.BRAND_DOCS_URL == "https://notes.example.org/docs"
        assert reloaded.BRAND_CONTACT_URL == "https://notes.example.org/contact"
        assert (
            reloaded.apply_brand("See {{BRAND_DOCS_URL}}.")
            == "See https://notes.example.org/docs."
        )


def test_env_overrides(reload_brand) -> None:
    reloaded = reload_brand(
        BRAND_NAME="Other Notes", BRAND_SITE_URL="https://example.test/"
    )
    assert reloaded.BRAND_NAME == "Other Notes"
    assert reloaded.BRAND_SITE_URL == "https://example.test"
    assert reloaded.BRAND_DOCS_URL == "https://example.test/docs"


def test_defaults_without_env(reload_brand) -> None:
    assert reload_brand(BRAND_NAME="   ").BRAND_NAME == brand.DEFAULT_BRAND_NAME
