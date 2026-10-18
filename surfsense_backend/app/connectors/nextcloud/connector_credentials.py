"""The Nextcloud connector's stored settings: server, account and app password.

The app password is encrypted with the server's SECRET_KEY the same way the
OAuth connectors store their tokens (``_token_encrypted``); only the
ciphertext ever leaves the backend. Rows are written by the Login Flow
(``auth_method: login_flow``); rows saved earlier from a pasted app password
have no ``auth_method`` and keep working while they name the configured server.
"""

from __future__ import annotations

from typing import Any

from app.config import config
from app.utils.oauth_security import TokenEncryption

from .client import NextcloudAccount
from .server_address import configured_server_url, normalize_server_url

SECRET_FIELD = "app_password"
LOGIN_FLOW = "login_flow"


class InvalidNextcloudSettingsError(ValueError):
    """The stored connection cannot be used."""


class NextcloudReconnectNeededError(InvalidNextcloudSettingsError):
    """The connection is for another server (or none is configured): connect again."""


def _cipher() -> TokenEncryption:
    if not config.SECRET_KEY:
        raise InvalidNextcloudSettingsError(
            "SECRET_KEY is not configured on this server"
        )
    return TokenEncryption(config.SECRET_KEY)


def stored_config(
    server_url: str,
    username: str,
    app_password: str,
    *,
    login_name: str | None = None,
    display_name: str | None = None,
) -> dict[str, Any]:
    """The connector ``config`` to persist, with the app password encrypted."""
    username = (username or "").strip()
    if not username:
        raise InvalidNextcloudSettingsError("The Nextcloud account has no user id")
    if not app_password or not app_password.strip():
        raise InvalidNextcloudSettingsError("Nextcloud sent no app password")
    server = normalize_server_url(server_url)
    if server is None:
        raise InvalidNextcloudSettingsError("The Nextcloud address is not a plain URL")
    return {
        "server_url": server,
        "username": username,
        "login_name": (login_name or "").strip() or username,
        "display_name": (display_name or "").strip() or username,
        "auth_method": LOGIN_FLOW,
        SECRET_FIELD: _cipher().encrypt_token(app_password.strip()),
        "_token_encrypted": True,
    }


def stored_account(connector_config: dict[str, Any] | None) -> NextcloudAccount:
    """The account a row holds, whichever server it names."""
    cfg = connector_config or {}
    secret = cfg.get(SECRET_FIELD) or ""
    if cfg.get("_token_encrypted") and secret:
        secret = _cipher().decrypt_token(secret)
    if not cfg.get("server_url") or not cfg.get("username") or not secret:
        raise NextcloudReconnectNeededError(
            "This Nextcloud connection is incomplete; connect again"
        )
    return NextcloudAccount(
        base_url=cfg["server_url"],
        username=cfg["username"],
        app_password=secret,
        login_name=cfg.get("login_name") or None,
    )


def account_from_config(connector_config: dict[str, Any] | None) -> NextcloudAccount:
    """The account to use; only ever one on the configured server."""
    account = stored_account(connector_config)
    if account.base_url != configured_server_url():
        raise NextcloudReconnectNeededError(
            "This Nextcloud connection is for another server; connect again"
        )
    return account
