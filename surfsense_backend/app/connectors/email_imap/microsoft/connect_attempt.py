"""What one Microsoft connect carries from its start to Microsoft's callback.

Who is connecting, from which workspace, where to return, the ``state`` and
the PKCE ``code_verifier`` live in one signed, HttpOnly cookie on the browser
that pressed "Connect a Microsoft account", sent only with the callback
request. A callback opened in any other browser (a forged or replayed link,
or someone luring another person through Microsoft's consent with their own
link) has no matching cookie and is refused, so a mailbox can only ever be
linked to the account of the person who started the connect. Nothing is
stored server-side.

``state`` starts with the workspace id (not secret) so even a refused
callback can land on that workspace's connectors page to say why.

The phone app's browser hand-off (``app/app_handoff``) is the one start that
does not happen in the person's own browser: its page sets this cookie in
whichever browser opens the hand-off link, so the cookie alone cannot tell
whose mailbox comes back. Such an attempt (``app``) therefore also carries
the app's PKCE challenge (``app_challenge``), and its callback saves nothing:
the mailbox waits, encrypted, until the app that asked for the link confirms
it with its own bearer token and the verifier behind that challenge
(``app_handoff/routes.complete_handoff``).
"""

from __future__ import annotations

import base64
import hashlib
import secrets
import time
from dataclasses import dataclass
from typing import Any

import jwt

from app.auth_oidc.return_path import safe_return_path

from .outcomes import EXPIRED, ConnectRefusedError

COOKIE_NAME = "notes_ms_mail_connect"
# How long a person has between pressing the button and finishing on
# Microsoft's pages (MFA included) before the connect must restart.
LIFETIME_SECONDS = 600
_AUDIENCE = "soloclef-notes:microsoft-mail-connect"
_ALGORITHM = "HS256"


def own_page(requested: str | None) -> str | None:
    """``requested`` if it is a page of this site, else ``None``."""
    if not requested:
        return None
    return requested if safe_return_path(requested) == requested else None


@dataclass(frozen=True)
class ConnectAttempt:
    state: str
    code_verifier: str
    user_id: str
    workspace_id: int
    return_path: str | None = None
    #: Started by the phone app's browser hand-off (app_handoff): the callback
    #: sends the sheet back to the app's address instead of a page of the site.
    app: bool = False
    #: The app's S256 challenge (app attempts only): the callback holds the
    #: mailbox for the app to confirm with the verifier behind it.
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
            return_path=None if app else own_page(return_path),
            app=app,
            app_challenge=app_challenge if app else None,
        )

    @property
    def code_challenge(self) -> str:
        digest = hashlib.sha256(self.code_verifier.encode("ascii")).digest()
        return base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")

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
        raise ConnectRefusedError(
            EXPIRED, "no Microsoft connect in progress on this browser"
        )
    try:
        claims = jwt.decode(
            sealed,
            secret,
            algorithms=[_ALGORITHM],
            audience=_AUDIENCE,
            options={"require": ["exp", "aud"]},
        )
    except jwt.PyJWTError as exc:
        raise ConnectRefusedError(EXPIRED, "connect cookie expired or forged") from exc
    # Compared as bytes: compare_digest refuses a str with non-ASCII
    # characters (TypeError), and the returned state is whatever the URL says.
    if not secrets.compare_digest(
        str(claims.get("state", "")).encode(), returned_state.encode()
    ):
        raise ConnectRefusedError(EXPIRED, "state does not match this browser")
    return ConnectAttempt(
        state=claims["state"],
        code_verifier=claims["cv"],
        user_id=claims["uid"],
        workspace_id=int(claims["ws"]),
        return_path=own_page(claims.get("rp")),
        app=claims.get("app") is True,
        app_challenge=_challenge(claims.get("ac"))
        if claims.get("app") is True
        else None,
    )


def _challenge(value: object) -> str | None:
    return value if isinstance(value, str) and value else None


def workspace_named_in(state: str | None) -> int | None:
    """The workspace id a (possibly forged) state starts with, for landing only."""
    head = (state or "").split(".", 1)[0]
    # ASCII digits only: str.isdigit() also accepts "²" (int() then fails)
    # and other scripts' digits (int() reads them as a different workspace).
    if head.isascii() and head.isdigit() and len(head) < 12:
        return int(head)
    return None
