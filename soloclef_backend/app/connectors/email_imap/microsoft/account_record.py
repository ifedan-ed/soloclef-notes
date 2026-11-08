"""A Microsoft mailbox as ``config["accounts"]`` stores it: tokens encrypted.

It sits in the same list as password accounts, with the same ``_encrypted``
marker, so the generic connector update path refuses plaintext the same way
(``credentials.assert_accounts_are_encrypted``). Tokens are encrypted with
the server's ``SECRET_KEY`` like the app passwords; only ciphertext is saved.
``host``/``port`` are for display: the token only ever goes to
``settings.IMAP_HOST`` (see ``imap_sign_in``).
"""

from __future__ import annotations

import uuid
from datetime import UTC, datetime
from typing import Any

from app.utils.oauth_security import TokenEncryption

from ..credentials import ENCRYPTED_MARKER, InvalidEmailAccountError
from .mailbox_identity import Mailbox
from .settings import IMAP_HOST, IMAP_PORT
from .token_endpoint import TokenGrant

AUTH_FIELD = "auth"
MICROSOFT = "microsoft"
NEEDS_RECONNECT = "needs_reconnect"
_ACCESS = "access_token"
_REFRESH = "refresh_token"
_EXPIRES = "access_token_expires_at"
_SUBJECT = "microsoft_subject"


def is_microsoft(account: dict[str, Any]) -> bool:
    return account.get(AUTH_FIELD) == MICROSOFT


def _cipher(secret_key: str) -> TokenEncryption:
    if not secret_key:
        raise InvalidEmailAccountError("SECRET_KEY is not configured on this server")
    return TokenEncryption(secret_key)


def _token_fields(grant: TokenGrant, *, secret_key: str) -> dict[str, Any]:
    cipher = _cipher(secret_key)
    fields: dict[str, Any] = {
        _ACCESS: cipher.encrypt_token(grant.access_token),
        _EXPIRES: grant.expires_at.isoformat(),
        NEEDS_RECONNECT: False,
    }
    if grant.refresh_token:
        fields[_REFRESH] = cipher.encrypt_token(grant.refresh_token)
    return fields


def new_mailbox_record(
    mailbox: Mailbox,
    grant: TokenGrant,
    *,
    secret_key: str,
    now: datetime | None = None,
) -> dict[str, Any]:
    """A record ready to append to ``config["accounts"]``."""
    return {
        "id": str(uuid.uuid4()),
        "label": mailbox.address,
        "host": IMAP_HOST,
        "port": IMAP_PORT,
        "use_tls": True,
        "username": mailbox.address,
        AUTH_FIELD: MICROSOFT,
        _SUBJECT: mailbox.subject,
        **_token_fields(grant, secret_key=secret_key),
        ENCRYPTED_MARKER: True,
        "created_at": (now or datetime.now(UTC)).isoformat(),
    }


def is_same_mailbox(account: dict[str, Any], mailbox: Mailbox) -> bool:
    """Whether connecting ``mailbox`` again should update ``account``."""
    if not is_microsoft(account):
        return False
    if mailbox.subject.strip(":") and account.get(_SUBJECT) == mailbox.subject:
        return True
    return str(account.get("username", "")).lower() == mailbox.address.lower()


def reconnected(
    account: dict[str, Any], mailbox: Mailbox, grant: TokenGrant, *, secret_key: str
) -> dict[str, Any]:
    """The same mailbox (id, label, added date) with the new grant."""
    return {
        **account,
        "username": mailbox.address,
        _SUBJECT: mailbox.subject,
        **_token_fields(grant, secret_key=secret_key),
        ENCRYPTED_MARKER: True,
    }


def refreshed_fields(grant: TokenGrant, *, secret_key: str) -> dict[str, Any]:
    """What to merge into the record after a refresh."""
    return _token_fields(grant, secret_key=secret_key)


def stored_access_token(
    account: dict[str, Any], *, secret_key: str
) -> tuple[str | None, datetime | None]:
    """The access token and when it lapses; ``(None, None)`` if there is none.

    Raises ``ValueError`` when the ciphertext no longer decrypts.
    """
    secret = account.get(_ACCESS)
    if not secret:
        return None, None
    try:
        expires_at = datetime.fromisoformat(str(account.get(_EXPIRES)))
    except ValueError:
        return None, None
    if expires_at.tzinfo is None:
        expires_at = expires_at.replace(tzinfo=UTC)
    return _cipher(secret_key).decrypt_token(secret), expires_at


def stored_refresh_token(account: dict[str, Any], *, secret_key: str) -> str:
    """Raises ``ValueError`` when missing or no longer decryptable."""
    secret = account.get(_REFRESH)
    if not secret or not account.get(ENCRYPTED_MARKER):
        raise ValueError("no stored refresh token")
    return _cipher(secret_key).decrypt_token(secret)
