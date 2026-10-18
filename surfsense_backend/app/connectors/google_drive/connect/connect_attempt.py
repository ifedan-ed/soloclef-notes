"""What one Google Drive connect carries from its start to Google's callback.

The same shape as a Microsoft mailbox connect
(``app/connectors/email_imap/microsoft/connect_attempt.py``): who is
connecting, from which workspace, where to return, the ``state`` and the
PKCE ``code_verifier`` live in one signed, HttpOnly cookie on the browser
that pressed Connect, sent only with the callback request. A callback opened
in any other browser (a consent link someone started in their own account
and sent to another person, or a replayed callback) has no matching cookie
and is refused, so a Drive can only be linked to the account of the person
who started the connect. Before, all of this rode in the ``state`` itself:
signed but readable, the PKCE verifier included, and accepted from any
browser. Nothing is stored server-side.

``state`` starts with the workspace id (not secret) so even a refused
callback lands on that workspace's connectors page.

The phone app's browser hand-off (``app/app_handoff``) sets this cookie in
whichever browser opened the hand-off link, so for those attempts (``app``)
the cookie alone cannot say whose Drive comes back: the attempt also carries
the app's PKCE challenge (``app_challenge``) and its callback saves nothing;
the grant waits, encrypted (``held_grant.py``), until the app that asked for
the link confirms it with its own bearer token and the verifier behind that
challenge (``app_handoff/routes.complete_handoff``).

Limits: ``LIFETIME_SECONDS`` (10 minutes from Connect to Google's answer);
custom/README.md, "Security fixes of 2026-10-11".
"""

from __future__ import annotations

import secrets
import time
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlsplit

import jwt

COOKIE_NAME = "notes_drive_connect"
#: How long a person has between pressing Connect and finishing on Google's
#: pages before the connect must restart: 10 minutes, as for Microsoft.
LIFETIME_SECONDS = 600
_AUDIENCE = "soloclef-notes:google-drive-connect"
_ALGORITHM = "HS256"


class AttemptRefusedError(Exception):
    """The callback is not the one this browser started (or it ran out)."""


def safe_return_path(path: str | None) -> str | None:
    """A path of this site (``/dashboard/3/...``), or ``None``."""
    if not path or not path.startswith("/") or path.startswith("//") or "\\" in path:
        return None
    return path


@dataclass(frozen=True)
class ConnectAttempt:
    state: str
    code_verifier: str
    user_id: str
    workspace_id: int
    return_path: str | None = None
    #: Started by the phone app's browser hand-off: the callback holds the
    #: grant for the app to confirm and sends the sheet back to the app.
    app: bool = False
    #: The app's S256 challenge (app attempts only).
    app_challenge: str | None = None

    @classmethod
    def start(
        cls,
        *,
        user_id: Any,
        workspace_id: int,
        return_path: str | None = None,
        app: bool = False,
        app_challenge: str | None = None,
    ) -> ConnectAttempt:
        return cls(
            state=f"{int(workspace_id)}.{secrets.token_urlsafe(32)}",
            # RFC 7636: 43-128 characters; 64 random bytes encode to 86.
            code_verifier=secrets.token_urlsafe(64),
            user_id=str(user_id),
            workspace_id=int(workspace_id),
            return_path=None if app else safe_return_path(return_path),
            app=app,
            app_challenge=app_challenge if app else None,
        )

    def seal(self, secret: str) -> str:
        """The cookie value: this attempt, signed with the app's SECRET_KEY."""
        now = int(time.time())
        claims = {
            "aud": _AUDIENCE,
            "state": self.state,
            "cv": self.code_verifier,
            "uid": self.user_id,
            "ws": self.workspace_id,
            "rp": self.return_path,
            "app": self.app,
            "ac": self.app_challenge,
            "iat": now,
            "exp": now + LIFETIME_SECONDS,
        }
        return jwt.encode(claims, secret, algorithm=_ALGORITHM)


def reopen(
    sealed: str | None, returned_state: str | None, secret: str
) -> ConnectAttempt:
    """The attempt this browser started, if the callback's state is its own."""
    if not sealed or not returned_state:
        raise AttemptRefusedError("no Drive connect in progress on this browser")
    try:
        claims = jwt.decode(
            sealed,
            secret,
            algorithms=[_ALGORITHM],
            audience=_AUDIENCE,
            options={"require": ["exp", "aud"]},
        )
    except jwt.PyJWTError as exc:
        raise AttemptRefusedError("connect cookie expired or forged") from exc
    # Bytes: compare_digest refuses a str with non-ASCII characters, and the
    # returned state is whatever the URL says.
    if not secrets.compare_digest(
        str(claims.get("state", "")).encode(), returned_state.encode()
    ):
        raise AttemptRefusedError("state does not match this browser")
    app = claims.get("app") is True
    challenge = claims.get("ac")
    return ConnectAttempt(
        state=claims["state"],
        code_verifier=claims["cv"],
        user_id=claims["uid"],
        workspace_id=int(claims["ws"]),
        return_path=safe_return_path(claims.get("rp")),
        app=app,
        app_challenge=challenge
        if app and isinstance(challenge, str) and challenge
        else None,
    )


def workspace_named_in(state: str | None) -> int | None:
    """The workspace id a (possibly forged) state starts with, for landing only."""
    head = (state or "").split(".", 1)[0]
    # ASCII digits only: str.isdigit() also accepts "²" and other scripts' digits.
    if head.isascii() and head.isdigit() and len(head) < 12:
        return int(head)
    return None


def cookie_path(redirect_uri: str | None) -> str:
    """Google's callback path: the only request the cookie goes with."""
    return urlsplit(redirect_uri or "").path or "/"


def cookie_secure(redirect_uri: str | None) -> bool:
    return (redirect_uri or "").startswith("https://")
