"""A Drive grant as a connector row stores it: tokens and client secret encrypted."""

from __future__ import annotations

from typing import Any

from app.config import config
from app.utils.oauth_security import TokenEncryption

_SECRET_FIELDS = ("token", "refresh_token", "client_secret")


def encrypted(grant: dict[str, Any]) -> dict[str, Any]:
    if not config.SECRET_KEY:
        raise ValueError("SECRET_KEY must be set to store Google tokens")
    encryption = TokenEncryption(config.SECRET_KEY)
    stored = dict(grant)
    for name in _SECRET_FIELDS:
        if stored.get(name):
            stored[name] = encryption.encrypt_token(stored[name])
    stored["_token_encrypted"] = True
    return stored


def replace_grant(
    previous: dict[str, Any] | None, grant: dict[str, Any], account_email: str | None
) -> dict[str, Any]:
    """The row's config after a (re)connect: new tokens, the person's picks kept.

    Picks belong to one Google account, so they are dropped when a different
    account connects.
    """
    kept = dict(previous or {})
    same_account = account_email and kept.get("account_email") == account_email
    if not same_account:
        for name in ("selected_files", "selected_folders", "folder_tokens"):
            kept.pop(name, None)
    kept.update(encrypted(grant))
    kept["account_email"] = account_email
    kept["auth_expired"] = False
    return kept
