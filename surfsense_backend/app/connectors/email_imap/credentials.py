"""Per-account IMAP credential storage: encrypted app passwords in a single
``SearchSourceConnector.config["accounts"]`` list.

Follows the same scheme as the Nextcloud connector
(``app/connectors/nextcloud/connector_credentials.py``): the app password is
encrypted with the server's ``SECRET_KEY`` via
``app.utils.oauth_security.TokenEncryption``; only ciphertext is persisted.
"""

from __future__ import annotations

import ipaddress
import uuid
from datetime import UTC, datetime
from typing import Any

from app.config import config
from app.utils.crawl.net_guard import is_publicly_routable
from app.utils.oauth_security import TokenEncryption

SECRET_FIELD = "app_password"
ENCRYPTED_MARKER = "_encrypted"


def _as_checkable_url(host: str) -> str:
    """``host`` wrapped as a URL ``is_publicly_routable`` can parse.

    A bare IPv6 literal (``2001:db8::1``, no brackets) needs bracketing
    first or ``urlsplit`` reads part of the address as a port.
    """
    try:
        ipaddress.IPv6Address(host)
    except ValueError:
        return f"https://{host}/"
    return f"https://[{host}]/"


def _assert_host_is_reachable(host: str) -> None:
    """Reject a host (or anything it resolves to) that is not publicly
    routable — loopback, RFC1918, link-local (including the cloud metadata
    address), CGNAT, IPv6 unique-local, multicast, or an internal Docker
    service name (which resolves to a private container address and is
    caught the same way, without a separate name check).

    This is a save-time check, not the actual network guarantee: DNS can
    still answer differently by the time a connection is made, which is why
    ``ReadOnlyImapClient`` (``app.connectors.email_imap.client``) repeats
    and pins this check at connect time. Reuses the same guard the crawler's
    SSRF check uses (``app.utils.crawl.net_guard``) rather than a new one.
    """
    if not is_publicly_routable(_as_checkable_url(host)):
        raise InvalidEmailAccountError(
            "This IMAP host could not be reached (private, link-local, or "
            "unresolvable addresses are not allowed)"
        )


class InvalidEmailAccountError(ValueError):
    """The account settings cannot be used."""


def _cipher() -> TokenEncryption:
    if not config.SECRET_KEY:
        raise InvalidEmailAccountError("SECRET_KEY is not configured on this server")
    return TokenEncryption(config.SECRET_KEY)


def new_account_record(
    *,
    label: str,
    host: str,
    port: int,
    use_tls: bool,
    username: str,
    app_password: str,
) -> dict[str, Any]:
    """An encrypted account record ready to append to ``config["accounts"]``."""
    label = (label or "").strip()
    host = (host or "").strip()
    username = (username or "").strip()
    if not host:
        raise InvalidEmailAccountError("IMAP host is required")
    _assert_host_is_reachable(host)
    if not username:
        raise InvalidEmailAccountError("Username is required")
    if not app_password or not app_password.strip():
        raise InvalidEmailAccountError("Password is required")
    if not (1 <= int(port) <= 65535):
        raise InvalidEmailAccountError("Port must be between 1 and 65535")
    return {
        "id": str(uuid.uuid4()),
        "label": label or username,
        "host": host,
        "port": int(port),
        "use_tls": bool(use_tls),
        "username": username,
        SECRET_FIELD: _cipher().encrypt_token(app_password.strip()),
        ENCRYPTED_MARKER: True,
        "created_at": datetime.now(UTC).isoformat(),
    }


def decrypt_password(account: dict[str, Any]) -> str:
    """The plaintext app password for one stored account record."""
    secret = account.get(SECRET_FIELD) or ""
    if not account.get(ENCRYPTED_MARKER) or not secret:
        raise InvalidEmailAccountError(
            "This account's credentials are missing or not encrypted; re-add it"
        )
    return _cipher().decrypt_token(secret)


def public_account(account: dict[str, Any]) -> dict[str, Any]:
    """The account view safe to return over the API: never a password or token.

    ``auth`` is "password" (the add-account form) or "microsoft" (connected
    through Microsoft's sign-in, ``app/connectors/email_imap/microsoft``);
    ``needs_reconnect`` is set when Microsoft stopped accepting the grant.
    """
    return {
        "id": account.get("id"),
        "label": account.get("label"),
        "host": account.get("host"),
        "port": account.get("port"),
        "use_tls": account.get("use_tls", True),
        "username": account.get("username"),
        "created_at": account.get("created_at"),
        "auth": account.get("auth") or "password",
        "needs_reconnect": bool(account.get("needs_reconnect")),
    }


def accounts_from_config(cfg: dict[str, Any] | None) -> list[dict[str, Any]]:
    accounts = (cfg or {}).get("accounts")
    if not isinstance(accounts, list):
        return []
    return [a for a in accounts if isinstance(a, dict)]


def public_config(cfg: dict[str, Any] | None) -> dict[str, Any]:
    """The connector config as the generic connector routes return it.

    Every account is reduced to :func:`public_account`, for every caller,
    the owner included: an encrypted password or token copied out of an API
    response could otherwise be written into another connection and used
    there (the server would decrypt it). Nothing in the app needs the
    ciphertext over the API; the dedicated routes return the same public view.
    """
    out = dict(cfg or {})
    if "accounts" in out:
        out["accounts"] = [public_account(a) for a in accounts_from_config(out)]
    return out


def keep_stored_accounts(
    incoming: dict[str, Any], stored: dict[str, Any] | None
) -> dict[str, Any]:
    """``incoming`` with ``accounts`` put back to what is stored.

    The generic connector update may change other config keys, but never the
    mailboxes: only the dedicated Email (IMAP) and Microsoft routes write
    accounts, and they build every record themselves from a password or a
    Microsoft sign-in, never from a record the caller sends.
    """
    out = dict(incoming)
    if "accounts" in (stored or {}):
        out["accounts"] = (stored or {})["accounts"]
    else:
        out.pop("accounts", None)
    return out


def assert_accounts_are_encrypted(accounts: Any) -> None:
    """Guard for the *generic* connector-config write path.

    The generic ``PUT /connectors/{id}`` endpoint accepts an arbitrary
    ``config`` dict for any connector type. For Email (IMAP) that must never
    be able to introduce or overwrite a plaintext app password — only the
    dedicated ``/connectors/email-imap/*`` routes (which call
    :func:`new_account_record`) are allowed to write secrets. Raise if the
    caller is trying to write an account that isn't already encrypted.

    This is the schema-level backstop. The routes go further: the generic
    create refuses any accounts and the generic update keeps the stored ones
    (:func:`keep_stored_accounts`), so even an encrypted record copied from
    someone else's connection can't be written
    (``app/routes/search_source_connectors_routes.py``).
    """
    if accounts is None:
        return
    if not isinstance(accounts, list):
        raise ValueError("config.accounts must be a list")
    for entry in accounts:
        if not isinstance(entry, dict) or not entry.get(ENCRYPTED_MARKER):
            raise ValueError(
                "Email (IMAP) accounts can only be added, tested, or removed "
                "through the dedicated connector endpoints, not the generic "
                "connector config update."
            )
