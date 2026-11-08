"""The secrets one sign-in carries from its start to its callback.

``state`` goes to the provider and comes back in the callback URL; ``nonce``
goes into the id_token; the PKCE ``code_verifier`` never leaves Notes until
the token exchange. All three are kept in one short-lived, signed, HttpOnly
cookie on the browser that started the sign-in, so a callback opened in any
other browser (a forged or replayed link) has no cookie to match and is
refused. Nothing is stored server-side.

A sign-in the phone app started (``app_sign_in.py``) also carries the app's
return address, its PKCE challenge and its device label, so the callback
knows to end with a one-time code for the app instead of cookies.
"""

from __future__ import annotations

import base64
import hashlib
import secrets
import time
from dataclasses import dataclass

import jwt

from app.auth_oidc.app_sign_in import AppRequest
from app.auth_oidc.errors import INVALID_STATE, OidcLoginError
from app.auth_oidc.return_path import DEFAULT_RETURN_PATH, safe_return_path

COOKIE_NAME = "notes_oidc_login"
COOKIE_PATH = "/auth/oidc"
# How long a person has between pressing the button and finishing at the
# provider (typing the emailed code included) before the sign-in must restart.
LIFETIME_SECONDS = 600
_AUDIENCE = "soloclef-notes:oidc-login"
_ALGORITHM = "HS256"


@dataclass(frozen=True)
class LoginAttempt:
    state: str
    nonce: str
    code_verifier: str
    return_to: str = DEFAULT_RETURN_PATH
    #: Set when the phone app started this sign-in.
    app: AppRequest | None = None

    @classmethod
    def start(
        cls, return_to: str | None = None, app: AppRequest | None = None
    ) -> LoginAttempt:
        return cls(
            state=secrets.token_urlsafe(32),
            nonce=secrets.token_urlsafe(32),
            # RFC 7636: 43-128 characters; 64 random bytes encode to 86.
            code_verifier=secrets.token_urlsafe(64),
            return_to=safe_return_path(return_to),
            app=app,
        )

    @property
    def code_challenge(self) -> str:
        digest = hashlib.sha256(self.code_verifier.encode("ascii")).digest()
        return base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")

    def seal(self, secret: str) -> str:
        """The cookie value: this attempt, signed with the app's SECRET_KEY."""
        claims = {
            "aud": _AUDIENCE,
            "state": self.state,
            "nonce": self.nonce,
            "cv": self.code_verifier,
            "rt": self.return_to,
        }
        if self.app is not None:
            claims["ap"] = {
                "r": self.app.redirect,
                "c": self.app.challenge,
                "d": self.app.device,
                "ca": self.app.choose_account,
            }
        now = int(time.time())
        claims.update(iat=now, exp=now + LIFETIME_SECONDS)
        return jwt.encode(claims, secret, algorithm=_ALGORITHM)


def _app_of(claims: dict) -> AppRequest | None:
    raw = claims.get("ap")
    if not isinstance(raw, dict):
        return None
    try:
        return AppRequest(
            redirect=str(raw["r"]),
            challenge=str(raw["c"]),
            device=str(raw.get("d") or ""),
            choose_account=bool(raw.get("ca")),
        )
    except KeyError as exc:
        raise OidcLoginError(
            INVALID_STATE, "sign-in cookie names no app request"
        ) from exc


def reopen(sealed: str | None, returned_state: str | None, secret: str) -> LoginAttempt:
    """The attempt this browser started, if the callback's state is its own."""
    if not sealed or not returned_state:
        raise OidcLoginError(INVALID_STATE, "no sign-in in progress on this browser")
    try:
        claims = jwt.decode(
            sealed,
            secret,
            algorithms=[_ALGORITHM],
            audience=_AUDIENCE,
            options={"require": ["exp", "aud"]},
        )
    except jwt.PyJWTError as exc:
        raise OidcLoginError(INVALID_STATE, "sign-in cookie expired or forged") from exc
    if not secrets.compare_digest(str(claims.get("state", "")), returned_state):
        raise OidcLoginError(INVALID_STATE, "state does not match this browser")
    return LoginAttempt(
        state=claims["state"],
        nonce=claims["nonce"],
        code_verifier=claims["cv"],
        return_to=safe_return_path(claims.get("rt")),
        app=_app_of(claims),
    )
