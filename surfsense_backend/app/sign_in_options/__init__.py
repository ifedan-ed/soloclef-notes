"""How people can sign in to this server, for anyone who asks (no sign-in).

The phone app reads it before anyone has signed in: to check that an address
someone typed is a Soloclef server at all (``service``), and to offer only the
ways this server takes (``GET /api/v1/sign-in-options``, ``routes.py``). It
reports the same settings the website's login page is drawn from, read from
the same environment (``docker/.env``):

- ``password``: email and password (``AUTH_TYPE`` not ``GOOGLE``, and the
  ``REGISTRATION_ENABLED`` kill switch on). ``forgot_password`` goes with it:
  the reset routes are mounted beside the password ones. The kill switch is
  not a sign-up switch: off, it refuses every sign-in (``app.py``,
  ``registration_allowed``), so every way here is off with it.
- ``sign_up``: whether the website offers sign-up (``NEXT_PUBLIC_SIGNUPS_OPEN``
  exactly ``true``, the web's own switch, ``lib/signups.ts``; the website
  build gets the same ``docker/.env`` line from ``docker-compose.yml``); with
  it off the website offers "Request access" instead. A server closes
  sign-ups and keeps sign-in by leaving it off and refusing
  ``POST /auth/register`` at its proxy, which this code cannot see, so the
  two must agree (``self-hosting.mdx``, "Closing sign-ups").
- ``sso``: the OIDC sign-in, when it is mounted (``OIDC_ENABLED`` and every
  setting it needs, as ``app.py`` mounts it). ``label`` is the website's
  button (``OIDC_BUTTON_LABEL``), ``name`` the provider's name taken from it
  for sentences ("Example SSO can't be reached right now"), and ``app``
  whether the phone app's return address is allowed (``OIDC_APP_REDIRECTS``).

Nothing here is secret: no client id, no provider address. custom/README.md,
"Sign-in options".
"""

from __future__ import annotations

from collections.abc import Mapping

from app.auth_oidc.settings import OidcSettings

# The fixed answer that tells the app this is a Soloclef server, whatever the
# operator calls the site (BRAND_NAME may be anything).
SERVICE = "soloclef"

# The phone app's own return address (app/auth_oidc/settings.py's default).
APP_RETURN = "soloclef://auth"

# The website's words when OIDC_BUTTON_LABEL is empty
# (surfsense_web/components/providers/runtime-config.server.tsx).
DEFAULT_SSO_LABEL = "Continue with single sign-on"
DEFAULT_SSO_NAME = "single sign-on"

# A button's opening words, dropped to leave the provider's name.
_LABEL_OPENINGS = ("continue with ", "sign in with ", "log in with ")


def provider_name(label: str) -> str:
    """``"Continue with Example SSO"`` -> ``"Example SSO"``; any other wording -> "single sign-on"."""
    text = " ".join(label.split())
    for opening in _LABEL_OPENINGS:
        if text.lower().startswith(opening) and text[len(opening) :].strip():
            return text[len(opening) :].strip()
    return DEFAULT_SSO_NAME


def sign_in_options(
    *,
    auth_type: str,
    registration_enabled: bool,
    oidc: OidcSettings,
    env: Mapping[str, str],
) -> dict:
    """What this server's sign-in offers, as ``GET /api/v1/sign-in-options`` answers it."""
    password = auth_type != "GOOGLE" and registration_enabled
    sso = None
    if registration_enabled and oidc.enabled and not oidc.missing():
        label = (
            " ".join((env.get("OIDC_BUTTON_LABEL") or "").split()) or DEFAULT_SSO_LABEL
        )
        sso = {
            "label": label,
            "name": provider_name(label),
            "app": APP_RETURN in oidc.app_redirects,
        }
    return {
        "service": SERVICE,
        "password": password,
        "forgot_password": password,
        "sign_up": password
        and (env.get("NEXT_PUBLIC_SIGNUPS_OPEN") or "").strip() == "true",
        "sso": sso,
    }
