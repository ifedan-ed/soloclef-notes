"""What the OIDC sign-in needs from ``docker/.env``.

Every value here is documented in ``custom/README.md`` ("Sign-in with
Authentik (OIDC)"). The client secret is read, never logged or echoed.
"""

from __future__ import annotations

import os
import re
from collections.abc import Mapping
from dataclasses import dataclass

CALLBACK_PATH = "/auth/oidc/callback"

# Sign-in starts and callbacks one client IP may make per minute, together.
# A sign-in uses two (the start and the callback). The limit is a guard
# against someone hammering the provider's token endpoint through Notes;
# when it is hit the login page says "Too many attempts".
DEFAULT_LOGIN_ATTEMPTS_PER_MINUTE = 30

# Where the phone app's sign-in may be sent back to (OIDC_APP_REDIRECTS, a
# comma- or space-separated list; app_sign_in.py). Only the app's own scheme
# by default: a start naming any other address is refused, so a forged start
# cannot carry a code anywhere else.
DEFAULT_APP_REDIRECTS = frozenset({"soloclef://auth"})


def _flag(value: str | None, default: bool) -> bool:
    if value is None or not value.strip():
        return default
    return value.strip().upper() in {"TRUE", "1", "YES", "ON"}


@dataclass(frozen=True)
class OidcSettings:
    enabled: bool
    discovery_url: str
    client_id: str
    client_secret: str = ""
    redirect_uri: str = ""
    frontend_url: str = ""
    scopes: tuple[str, ...] = ("openid", "email", "profile")
    # Stored as ``oauth_account.oauth_name``; changing it orphans every link.
    provider_name: str = "authentik"
    # A first sign-in with a verified address and no Notes account:
    # allow_signup (OIDC_ALLOW_SIGNUP=TRUE) makes an account for any address;
    # autocreate_domains (OIDC_AUTOCREATE_DOMAINS) makes one only for these
    # exact domains. Both off: refused ("No account for this address").
    # The rule is app/auth_oidc/account_creation_rule.py.
    allow_signup: bool = False
    autocreate_domains: frozenset[str] = frozenset()
    login_attempts_per_minute: int = DEFAULT_LOGIN_ATTEMPTS_PER_MINUTE
    # The phone app's allowed return addresses (app_sign_in.py).
    app_redirects: frozenset[str] = DEFAULT_APP_REDIRECTS

    def __repr__(self) -> str:  # keep the secret out of tracebacks and logs
        return (
            f"OidcSettings(enabled={self.enabled}, client_id={self.client_id!r}, "
            f"discovery_url={self.discovery_url!r})"
        )

    def missing(self) -> list[str]:
        """Names of the settings an enabled sign-in cannot start without."""
        required = {
            "OIDC_DISCOVERY_URL": self.discovery_url,
            "OIDC_CLIENT_ID": self.client_id,
            "OIDC_CLIENT_SECRET": self.client_secret,
            "BACKEND_URL or SURFSENSE_PUBLIC_URL": self.redirect_uri,
            "NEXT_FRONTEND_URL or SURFSENSE_PUBLIC_URL": self.frontend_url,
        }
        return [name for name, value in required.items() if not value]


def parse_domains(value: str | None) -> frozenset[str]:
    """``"example.com, @Example.org"`` -> ``{"example.com", "example.org"}``.

    Commas and/or spaces separate entries; case, a leading "@" and a trailing
    "." are ignored. No wildcards: each entry is one exact domain.
    """
    entries = (
        part.strip().lower().lstrip("@").rstrip(".")
        for part in re.split(r"[\s,]+", value or "")
    )
    return frozenset(entry for entry in entries if entry)


def parse_app_redirects(value: str | None) -> frozenset[str]:
    """``"soloclef://auth, soloclef-dev://auth"`` -> both; empty -> the default."""
    entries = frozenset(
        part.strip() for part in re.split(r"[\s,]+", value or "") if part.strip()
    )
    return entries or DEFAULT_APP_REDIRECTS


def load_oidc_settings(
    env: Mapping[str, str] | None = None,
    *,
    backend_url: str | None = None,
    frontend_url: str | None = None,
) -> OidcSettings:
    env = os.environ if env is None else env
    scopes = tuple((env.get("OIDC_SCOPES") or "openid email profile").split())
    if "openid" not in scopes:
        scopes = ("openid", *scopes)
    backend = (backend_url or "").rstrip("/")
    try:
        attempts = int(env.get("OIDC_LOGIN_ATTEMPTS_PER_MINUTE") or 0)
    except ValueError:
        attempts = 0
    return OidcSettings(
        enabled=_flag(env.get("OIDC_ENABLED"), default=False),
        discovery_url=(env.get("OIDC_DISCOVERY_URL") or "").strip(),
        client_id=(env.get("OIDC_CLIENT_ID") or "").strip(),
        client_secret=(env.get("OIDC_CLIENT_SECRET") or "").strip(),
        redirect_uri=f"{backend}{CALLBACK_PATH}" if backend else "",
        frontend_url=(frontend_url or "").rstrip("/"),
        scopes=scopes,
        provider_name=(env.get("OIDC_PROVIDER_NAME") or "authentik").strip(),
        allow_signup=_flag(env.get("OIDC_ALLOW_SIGNUP"), default=False),
        autocreate_domains=parse_domains(env.get("OIDC_AUTOCREATE_DOMAINS")),
        login_attempts_per_minute=attempts
        if attempts > 0
        else DEFAULT_LOGIN_ATTEMPTS_PER_MINUTE,
        app_redirects=parse_app_redirects(env.get("OIDC_APP_REDIRECTS")),
    )
