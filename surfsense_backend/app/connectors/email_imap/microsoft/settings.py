"""What connecting Microsoft mailboxes needs from ``docker/.env``, and its fixed limits.

Every value is documented in ``custom/docs/email-imap-connector.md``
("Microsoft accounts"). Until the client id and secret are set the feature is
off: the connector shows "Microsoft accounts aren't supported yet." and the
connect routes answer 503. The client secret is read, never logged or echoed.
"""

from __future__ import annotations

import os
import re
from collections.abc import Mapping
from dataclasses import dataclass
from urllib.parse import urlsplit

# Mounted under /api/v1 (app/app.py), so the redirect URI registered in
# Microsoft Entra is BACKEND_URL + /api/v1 + this path.
CALLBACK_PATH = "/auth/microsoft/mail/connector/callback"
_API_PREFIX = "/api/v1"

# Exchange Online's IMAP endpoint, for Microsoft 365 and Outlook.com alike.
# Fixed in code, never read from the stored account, so an access token can
# only ever be sent to Microsoft.
IMAP_HOST = "outlook.office365.com"
IMAP_PORT = 993
IMAP_SCOPE = "https://outlook.office.com/IMAP.AccessAsUser.All"
# openid + email + profile: the id_token names the mailbox. Microsoft puts
# `email` in it only with the email scope, and `preferred_username` (the
# fallback address) and `oid` (the stable account id) only with profile.
# offline_access: a refresh token, so the mailbox stays connected after the
# first hour.
SCOPES = ("openid", "email", "profile", "offline_access", IMAP_SCOPE)

# A stored access token with less than this left is refreshed before use, so
# an IMAP session never starts on a token about to lapse (they last ~1 hour).
REFRESH_MARGIN_SECONDS = 300
# One request to Microsoft's token endpoint (code exchange or refresh).
TOKEN_TIMEOUT_SECONDS = 10

DEFAULT_TENANT = "common"
LOGIN_HOST = "https://login.microsoftonline.com"
# "common", "organizations", "consumers", a tenant id or a verified domain.
_TENANT = re.compile(r"^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$")


@dataclass(frozen=True)
class MicrosoftMailSettings:
    client_id: str = ""
    client_secret: str = ""
    tenant: str = DEFAULT_TENANT
    redirect_uri: str = ""
    frontend_url: str = ""
    #: The app's SECRET_KEY: signs the connect cookie, encrypts the tokens.
    secret_key: str = ""

    def __repr__(self) -> str:  # keep the secrets out of tracebacks and logs
        return (
            f"MicrosoftMailSettings(client_id={self.client_id!r}, "
            f"tenant={self.tenant!r}, redirect_uri={self.redirect_uri!r})"
        )

    def missing(self) -> list[str]:
        """Names (never values) of the settings connecting cannot work without."""
        required = {
            "MICROSOFT_MAIL_CLIENT_ID": self.client_id,
            "MICROSOFT_MAIL_CLIENT_SECRET": self.client_secret,
            "MICROSOFT_MAIL_REDIRECT_URI or BACKEND_URL": self.redirect_uri,
            "NEXT_FRONTEND_URL or SURFSENSE_PUBLIC_URL": self.frontend_url,
            "SECRET_KEY": self.secret_key,
        }
        missing = [name for name, value in required.items() if not value]
        if not _TENANT.match(self.tenant):
            missing.append("MICROSOFT_MAIL_TENANT")
        return missing

    @property
    def configured(self) -> bool:
        return not self.missing()

    @property
    def authorize_url(self) -> str:
        return f"{LOGIN_HOST}/{self.tenant}/oauth2/v2.0/authorize"

    @property
    def token_url(self) -> str:
        return f"{LOGIN_HOST}/{self.tenant}/oauth2/v2.0/token"

    @property
    def cookie_path(self) -> str:
        """The callback's path: the only request the connect cookie goes with."""
        return urlsplit(self.redirect_uri).path or "/"

    @property
    def cookie_secure(self) -> bool:
        return self.redirect_uri.startswith("https://")


def load_microsoft_mail_settings(
    env: Mapping[str, str] | None = None,
    *,
    backend_url: str | None = None,
    frontend_url: str | None = None,
    secret_key: str | None = None,
) -> MicrosoftMailSettings:
    env = os.environ if env is None else env
    backend = (backend_url or "").rstrip("/")
    redirect = (env.get("MICROSOFT_MAIL_REDIRECT_URI") or "").strip()
    if not redirect and backend:
        redirect = f"{backend}{_API_PREFIX}{CALLBACK_PATH}"
    return MicrosoftMailSettings(
        client_id=(env.get("MICROSOFT_MAIL_CLIENT_ID") or "").strip(),
        client_secret=(env.get("MICROSOFT_MAIL_CLIENT_SECRET") or "").strip(),
        tenant=(env.get("MICROSOFT_MAIL_TENANT") or "").strip() or DEFAULT_TENANT,
        redirect_uri=redirect,
        frontend_url=(frontend_url or "").rstrip("/"),
        secret_key=secret_key or "",
    )


def current_settings() -> MicrosoftMailSettings:
    """This server's settings, read fresh from the environment each call."""
    from app.config import config

    return load_microsoft_mail_settings(
        backend_url=config.BACKEND_URL,
        frontend_url=config.NEXT_FRONTEND_URL,
        secret_key=config.SECRET_KEY,
    )
