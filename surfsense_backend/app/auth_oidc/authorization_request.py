"""Where the browser is sent to sign in at the provider."""

from __future__ import annotations

from urllib.parse import urlencode

from app.auth_oidc.discovery import ProviderMetadata
from app.auth_oidc.login_attempt import LoginAttempt
from app.auth_oidc.settings import OidcSettings


def authorization_url(
    settings: OidcSettings, provider: ProviderMetadata, attempt: LoginAttempt
) -> str:
    """Authorization-code request with PKCE (S256) and a nonce.

    The phone app's "Use a different address" asks for the account chooser
    with ``prompt=select_account`` (OpenID Connect Core 3.1.2.1). It is never
    ``prompt=login``: Authentik 2026.8 looped on that (PedsHub, 2026-10-08).
    Whether Example SSO honours ``select_account`` is checked on a device
    (spike S1); a provider that ignores it signs the same person in again,
    which the app's "Also sign out of Example SSO" then undoes.
    """
    query = {
        "response_type": "code",
        "client_id": settings.client_id,
        "redirect_uri": settings.redirect_uri,
        "scope": " ".join(settings.scopes),
        "state": attempt.state,
        "nonce": attempt.nonce,
        "code_challenge": attempt.code_challenge,
        "code_challenge_method": "S256",
    }
    if attempt.app is not None and attempt.app.choose_account:
        query["prompt"] = "select_account"
    separator = "&" if "?" in provider.authorization_endpoint else "?"
    return f"{provider.authorization_endpoint}{separator}{urlencode(query)}"
