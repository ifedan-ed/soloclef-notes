"""The signed cookie that ties a callback to the browser that started it."""

from __future__ import annotations

import time

import jwt
import pytest

from app.auth_oidc import login_attempt
from app.auth_oidc.errors import INVALID_STATE, OidcLoginError
from app.auth_oidc.login_attempt import LoginAttempt, reopen

pytestmark = pytest.mark.unit

SECRET = "unit-test-secret-key-of-reasonable-length"


def test_the_browser_that_started_gets_its_attempt_back():
    attempt = LoginAttempt.start()
    assert reopen(attempt.seal(SECRET), attempt.state, SECRET) == attempt


@pytest.mark.parametrize(
    ("cookie", "state"),
    [
        (None, "anything"),  # another browser: no cookie
        ("", "anything"),
        ("sealed", None),  # callback without state
    ],
)
def test_missing_pieces_are_refused(cookie, state):
    with pytest.raises(OidcLoginError) as refused:
        reopen(cookie, state, SECRET)
    assert refused.value.code == INVALID_STATE


def test_a_state_from_another_sign_in_is_refused():
    mine, theirs = LoginAttempt.start(), LoginAttempt.start()
    with pytest.raises(OidcLoginError) as refused:
        reopen(mine.seal(SECRET), theirs.state, SECRET)
    assert refused.value.code == INVALID_STATE


def test_a_cookie_signed_with_another_key_is_refused():
    attempt = LoginAttempt.start()
    with pytest.raises(OidcLoginError):
        reopen(attempt.seal("some-other-secret-key-entirely"), attempt.state, SECRET)


def test_an_expired_attempt_is_refused():
    attempt = LoginAttempt.start()
    stale = jwt.encode(
        {
            "aud": "soloclef-notes:oidc-login",
            "state": attempt.state,
            "nonce": attempt.nonce,
            "cv": attempt.code_verifier,
            "exp": int(time.time()) - 1,
        },
        SECRET,
        algorithm="HS256",
    )
    with pytest.raises(OidcLoginError) as refused:
        reopen(stale, attempt.state, SECRET)
    assert refused.value.code == INVALID_STATE


def test_lives_ten_minutes():
    assert login_attempt.LIFETIME_SECONDS == 600


def test_code_challenge_is_rfc7636_s256():
    # RFC 7636 Appendix B.
    attempt = LoginAttempt(
        state="s",
        nonce="n",
        code_verifier="dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
    )
    assert attempt.code_challenge == "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"


def test_every_attempt_is_unguessable_and_new():
    a, b = LoginAttempt.start(), LoginAttempt.start()
    assert len({a.state, a.nonce, b.state, b.nonce}) == 4
    assert 43 <= len(a.code_verifier) <= 128
