"""A Drive grant held for the phone app to confirm (the app hand-off's completion).

The callback of a connect the phone app started (``ConnectAttempt.app``)
does not save the grant: whoever opened the hand-off link went through
Google's consent, and only the app that asked for the link may say the Drive
is its person's (``app/app_handoff/routes.complete_handoff``). So the
callback seals what saving needs (Google's credentials and the account's
address) into one string encrypted with SECRET_KEY (``TokenEncryption``, as
the saved tokens are), which the hand-off's completion store keeps under the
completion's hash for ``COMPLETION_LIFETIME_SECONDS`` (5 minutes). The
Microsoft mailbox's twin is ``email_imap/microsoft/held_mailbox.py``.

Pure: no database, no FastAPI.
"""

from __future__ import annotations

import json
from typing import Any

from app.utils.oauth_security import TokenEncryption

_VERSION = 1


class HeldGrantError(ValueError):
    """The held grant no longer opens (another SECRET_KEY, or tampered with)."""


def seal(grant: dict[str, Any], email: str | None, *, secret_key: str) -> str:
    """Google's credentials and the account's address, encrypted."""
    payload = {"v": _VERSION, "grant": grant, "email": email}
    return TokenEncryption(secret_key).encrypt_token(json.dumps(payload))


def unseal(sealed: str, *, secret_key: str) -> tuple[dict[str, Any], str | None]:
    """What ``seal`` held, ready for the Drive connection's row."""
    try:
        data = json.loads(TokenEncryption(secret_key).decrypt_token(sealed))
        if data.get("v") != _VERSION:
            raise HeldGrantError("held grant of another version")
        grant = data["grant"]
        if not isinstance(grant, dict):
            raise HeldGrantError("held grant is not a grant")
        email = data.get("email")
    except HeldGrantError:
        raise
    except (ValueError, KeyError, TypeError, AttributeError) as exc:
        raise HeldGrantError("held grant does not open") from exc
    return grant, email if isinstance(email, str) and email else None
