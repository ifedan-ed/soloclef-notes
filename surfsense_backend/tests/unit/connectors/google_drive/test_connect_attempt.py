"""A Drive connect is bound to the browser that started it, and an app's grant is held sealed.

Pure pieces of connect_attempt.py and held_grant.py; the doors that use them
are tests/unit/app_handoff/test_routes.py.
"""

from __future__ import annotations

import time

import jwt
import pytest

from app.connectors.google_drive.connect import connect_attempt, held_grant
from app.connectors.google_drive.connect.connect_attempt import (
    AttemptRefusedError,
    ConnectAttempt,
    reopen,
)

pytestmark = pytest.mark.unit

SECRET = "test-secret-key-that-is-32-bytes-or-more"


def _attempt(**extra) -> ConnectAttempt:
    return ConnectAttempt.start(user_id="u-1", workspace_id=7, **extra)


def test_the_browser_that_started_it_gets_its_attempt_back() -> None:
    attempt = _attempt(return_path="/dashboard/7/notebooks/2")
    again = reopen(attempt.seal(SECRET), attempt.state, SECRET)
    assert again == attempt
    assert attempt.state.startswith("7.")
    assert len(attempt.code_verifier) >= 43


@pytest.mark.parametrize(
    "sealed_state",
    [
        (None, "7.x"),  # no cookie: another browser
        ("not-a-jwt", "7.x"),
    ],
)
def test_no_cookie_or_a_forged_one_is_refused(sealed_state) -> None:
    sealed, state = sealed_state
    with pytest.raises(AttemptRefusedError):
        reopen(sealed, state, SECRET)


def test_another_attempts_state_is_refused() -> None:
    mine, theirs = _attempt(), _attempt()
    with pytest.raises(AttemptRefusedError):
        reopen(mine.seal(SECRET), theirs.state, SECRET)
    with pytest.raises(AttemptRefusedError):
        reopen(mine.seal(SECRET), "7.é", SECRET)  # non-ASCII, from the URL


def test_a_cookie_signed_with_another_key_or_too_old_is_refused() -> None:
    attempt = _attempt()
    with pytest.raises(AttemptRefusedError):
        reopen(
            attempt.seal("another-secret-key-of-32-bytes-or-more"),
            attempt.state,
            SECRET,
        )
    old = jwt.decode(
        attempt.seal(SECRET),
        SECRET,
        algorithms=["HS256"],
        audience=connect_attempt._AUDIENCE,
    )
    old["exp"] = int(time.time()) - 1
    with pytest.raises(AttemptRefusedError):
        reopen(jwt.encode(old, SECRET, algorithm="HS256"), attempt.state, SECRET)


def test_a_return_path_to_another_site_is_dropped() -> None:
    assert _attempt(return_path="//evil.example/x").return_path is None
    assert _attempt(return_path="https://evil.example/").return_path is None


def test_an_app_attempt_carries_its_challenge_and_no_return_path() -> None:
    app = _attempt(app=True, app_challenge="c" * 43, return_path="/x")
    again = reopen(app.seal(SECRET), app.state, SECRET)
    assert again.app is True and again.app_challenge == "c" * 43
    assert again.return_path is None
    site = _attempt(app_challenge="c" * 43)
    assert site.app_challenge is None


def test_the_cookie_goes_only_with_googles_callback() -> None:
    uri = "https://notes.example/api/v1/auth/google/drive/connector/callback"
    assert (
        connect_attempt.cookie_path(uri)
        == "/api/v1/auth/google/drive/connector/callback"
    )
    assert connect_attempt.cookie_secure(uri) is True
    assert connect_attempt.cookie_secure("http://test/cb") is False


def test_the_workspace_a_state_names_is_read_for_landing_only() -> None:
    assert connect_attempt.workspace_named_in("12.abc") == 12
    assert connect_attempt.workspace_named_in("²3.abc") is None
    assert connect_attempt.workspace_named_in(None) is None


def test_a_held_grant_opens_only_with_the_key_it_was_sealed_with() -> None:
    grant = {"token": "ya29.access", "refresh_token": "1//refresh"}
    sealed = held_grant.seal(grant, "drive@example.org", secret_key=SECRET)
    assert "ya29" not in sealed and "drive@example.org" not in sealed
    assert held_grant.unseal(sealed, secret_key=SECRET) == (grant, "drive@example.org")
    with pytest.raises(held_grant.HeldGrantError):
        held_grant.unseal(sealed, secret_key="another-secret-key-of-32-bytes-or-more")
    with pytest.raises(held_grant.HeldGrantError):
        held_grant.unseal("tampered", secret_key=SECRET)
