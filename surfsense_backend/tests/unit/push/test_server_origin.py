"""Which server a push names as its sender (``data["server"]``): the address
phones are set to, SURFSENSE_PUBLIC_URL, in the form the app compares, so a
phone set to another server can ignore it."""

from __future__ import annotations

import logging

import pytest

from app import brand
from app.config import config
from app.push import server_origin
from app.push.server_origin import origin_of, push_origin_warning, this_server

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("address", "origin"),
    [
        ("https://notes.example.org", "https://notes.example.org"),
        # As the app's URL(...).origin reads it: no slash, no path, lower case.
        ("https://Notes.Example.org/", "https://notes.example.org"),
        ("https://notes.example.org/notes/?x=1#y", "https://notes.example.org"),
        ("  https://notes.example.org  ", "https://notes.example.org"),
        # A port is kept unless it is the scheme's own.
        ("http://localhost:3929", "http://localhost:3929"),
        ("https://notes.example.org:443", "https://notes.example.org"),
        ("http://notes.example.org:80/", "http://notes.example.org"),
        ("https://notes.example.org:8443", "https://notes.example.org:8443"),
        ("HTTPS://user:pw@notes.example.org", "https://notes.example.org"),
        # The app drops a trailing dot (address.ts, deepLinks.ts originOf).
        ("https://notes.example.org./", "https://notes.example.org"),
        ("https://10.0.0.5:8443", "https://10.0.0.5:8443"),
        # An international name goes out in ASCII, as URL.origin gives it and
        # as the app takes it (it refuses the letters, takes the xn-- form).
        ("https://bücher.example", "https://xn--bcher-kva.example"),
        ("https://BÜCHER.Example:443/x/", "https://xn--bcher-kva.example"),
        ("https://xn--bcher-kva.example", "https://xn--bcher-kva.example"),
        # Browsers' conversion (UTS #46), not Python's IDNA 2003 ("fass.de").
        ("https://faß.de", "https://xn--fa-hia.de"),
    ],
)
def test_the_origin_is_the_apps_form_of_the_address(address, origin):
    assert origin_of(address) == origin


@pytest.mark.parametrize(
    "address",
    [
        "",
        "   ",
        "notes.example.org",
        "/notes",
        "ftp://notes.example.org",
        "https://",
        "https://x:notaport",
        "https://notes.example.org:0",
        "https://notes.example.org:70000",
        # Hosts the app refuses (address.ts): it could never be set to them,
        # and its originOf makes nothing of them, so every push would drop.
        "https://my_notes.example.org",
        "http://[::1]:3929",
        "https://[2001:db8::1]",
        "https://-notes.example.org",
        "https://notes-.example.org",
        f"https://{'a' * 64}.example.org",
        f"https://{'a.' * 126}org",
        "https://1234",
        "https://1.2.3",
        "https://300.1.1.1",
        "https://b%C3%BCcher.example",
        "https://😀.example",
    ],
)
def test_an_address_the_app_could_not_match_gives_no_origin(address):
    assert origin_of(address) == ""


@pytest.fixture
def push_address(monkeypatch):
    """Set SURFSENSE_PUBLIC_URL as config read it, with every other address elsewhere."""

    def _set(address, *, push=True):
        monkeypatch.setattr(config, "PUSH_ENABLED", push)
        monkeypatch.setattr(config, "SURFSENSE_PUBLIC_URL", address)
        # A public site elsewhere, a website and an API of their own: none is
        # the address phones are set to, so none may be named.
        monkeypatch.setattr(brand, "BRAND_SITE_URL", "https://www.example.org")
        monkeypatch.setattr(config, "NEXT_FRONTEND_URL", "https://app.example.org")
        monkeypatch.setattr(config, "BACKEND_URL", "https://api.example.org")

    return _set


def test_this_server_is_the_address_phones_use_never_the_brand_site(push_address):
    # BRAND_SITE_URL names a marketing site on a server that has one; a push
    # naming it would be dropped by every phone set to the app's address.
    push_address("https://Notes.Example.org/")
    assert this_server() == "https://notes.example.org"


def test_this_server_stays_as_it_was_on_a_single_address_server(monkeypatch):
    # notes.example.com's docker/.env sets SURFSENSE_PUBLIC_URL and none of
    # BRAND_SITE_URL, NEXT_FRONTEND_URL, BACKEND_URL (checked 2026-10-11).
    monkeypatch.setattr(config, "SURFSENSE_PUBLIC_URL", "https://notes.example.com")
    assert this_server() == "https://notes.example.com"


@pytest.mark.parametrize(
    "address",
    [
        None,
        "",
        "http://localhost:3929",
        "http://notes.example.org",
        "https://my_notes.example.org",
    ],
)
def test_a_server_phones_could_not_match_names_none(push_address, address):
    # No key: the app shows the push as an older server's. An http origin
    # (or the brand site) would have it dropped on every phone.
    push_address(address)
    assert this_server() == ""


def test_an_international_server_is_named_in_ascii(push_address):
    push_address("https://bücher.example")
    assert this_server() == "https://xn--bcher-kva.example"


def test_no_warning_while_push_is_off_or_the_address_is_right(push_address):
    push_address("http://localhost:3929", push=False)
    assert push_origin_warning() == ""
    push_address("https://notes.example.org")
    assert push_origin_warning() == ""


@pytest.mark.parametrize(
    ("address", "says"),
    [
        (None, "SURFSENSE_PUBLIC_URL is not set"),
        ("", "SURFSENSE_PUBLIC_URL is not set"),
        (
            "http://localhost:3929",
            "SURFSENSE_PUBLIC_URL is http://localhost:3929, not https",
        ),
        ("https://my_notes.example.org", "not an address the phone app takes"),
    ],
)
def test_push_on_with_an_address_phones_cannot_match_is_a_startup_warning(
    push_address, address, says
):
    push_address(address)
    warning = push_origin_warning()
    assert says in warning
    assert "pushes name no server" in warning
    assert "the https address people type in the phone app" in warning


def test_the_warning_never_repeats_a_password_in_the_address(push_address):
    push_address("http://admin:hunter2@notes.example.org")
    warning = push_origin_warning()
    assert "http://notes.example.org" in warning
    assert "hunter2" not in warning
    assert "admin" not in warning


def test_the_startup_warning_is_logged_only_when_there_is_one(push_address, caplog):
    push_address("http://localhost:3929")
    with caplog.at_level(logging.WARNING, logger=server_origin.logger.name):
        server_origin.warn_about_push_origin()
    assert [r.levelno for r in caplog.records] == [logging.WARNING]
    assert "[startup] push: SURFSENSE_PUBLIC_URL is http://" in caplog.text

    caplog.clear()
    push_address("https://notes.example.org")
    with caplog.at_level(logging.WARNING, logger=server_origin.logger.name):
        server_origin.warn_about_push_origin()
    assert caplog.records == []
