"""A finished sign-in may land on a page of Notes, never on another site."""

from __future__ import annotations

import pytest

from app.auth_oidc.login_attempt import LoginAttempt, reopen
from app.auth_oidc.return_path import safe_return_path

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    "path",
    ["/dashboard", "/notebooks/12?tab=sources", "/share/abc#note-3"],
)
def test_a_page_of_notes_is_kept(path):
    assert safe_return_path(path) == path


@pytest.mark.parametrize(
    "path",
    [
        None,
        "",
        "dashboard",
        "//evil.example/",
        "/\\evil.example/",
        "https://evil.example/",
        "/x\\y",
        "javascript:alert(1)",
        "/login?error=x",
        "/auth/oidc/authorize-redirect",
        "/a\r\nSet-Cookie: x=y",
        "/" + "a" * 3000,
    ],
)
def test_anything_else_lands_on_the_dashboard(path):
    assert safe_return_path(path) == "/dashboard"


def test_the_return_path_travels_in_the_sealed_attempt():
    secret = "unit-test-secret-key-of-reasonable-length"
    attempt = LoginAttempt.start("/notebooks/7")
    assert reopen(attempt.seal(secret), attempt.state, secret).return_to == (
        "/notebooks/7"
    )
