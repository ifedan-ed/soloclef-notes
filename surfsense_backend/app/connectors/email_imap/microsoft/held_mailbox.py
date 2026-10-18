"""A connected mailbox held for the phone app to confirm (the app hand-off's completion).

The callback of a connect the phone app started (``ConnectAttempt.app``) does
not save the mailbox: whoever opened the hand-off link went through
Microsoft's consent, and only the app that asked for the link may say the
mailbox is its person's (``app/app_handoff/routes.complete_handoff``). So
the callback seals what saving needs (the mailbox's identity and Microsoft's
tokens) into one string encrypted with SECRET_KEY (``TokenEncryption``, as
the saved tokens are), which the hand-off's completion store keeps, stored
under the completion's hash, for ``COMPLETION_LIFETIME_SECONDS`` (5 minutes).

Pure: no database, no FastAPI.
"""

from __future__ import annotations

import json
from datetime import datetime

from app.utils.oauth_security import TokenEncryption

from .mailbox_identity import Mailbox
from .token_endpoint import TokenGrant

_VERSION = 1


class HeldMailboxError(ValueError):
    """The held mailbox no longer opens (another SECRET_KEY, or tampered with)."""


def seal(mailbox: Mailbox, grant: TokenGrant, *, secret_key: str) -> str:
    """The mailbox and its grant, encrypted: nothing in it reads without SECRET_KEY."""
    payload = {
        "v": _VERSION,
        "address": mailbox.address,
        "subject": mailbox.subject,
        "access_token": grant.access_token,
        "refresh_token": grant.refresh_token,
        "expires_at": grant.expires_at.isoformat(),
        "scopes": sorted(grant.scopes),
    }
    return TokenEncryption(secret_key).encrypt_token(json.dumps(payload))


def unseal(sealed: str, *, secret_key: str) -> tuple[Mailbox, TokenGrant]:
    """What ``seal`` held, ready for ``MicrosoftMailboxStore.save``."""
    try:
        data = json.loads(TokenEncryption(secret_key).decrypt_token(sealed))
        if data.get("v") != _VERSION:
            raise HeldMailboxError("held mailbox of another version")
        mailbox = Mailbox(address=str(data["address"]), subject=str(data["subject"]))
        grant = TokenGrant(
            access_token=str(data["access_token"]),
            refresh_token=data.get("refresh_token") or None,
            expires_at=datetime.fromisoformat(str(data["expires_at"])),
            scopes=frozenset(str(s) for s in data.get("scopes") or ()),
        )
    except HeldMailboxError:
        raise
    except (ValueError, KeyError, TypeError, AttributeError) as exc:
        raise HeldMailboxError("held mailbox does not open") from exc
    return mailbox, grant
