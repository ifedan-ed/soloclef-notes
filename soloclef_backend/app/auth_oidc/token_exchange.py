"""Trade the callback's one-time code for the provider's id_token."""

from __future__ import annotations

import logging

import httpx

from app.auth_oidc.discovery import ProviderDirectory
from app.auth_oidc.errors import EXCHANGE_FAILED, OidcLoginError
from app.auth_oidc.login_attempt import LoginAttempt
from app.auth_oidc.settings import OidcSettings

logger = logging.getLogger(__name__)


async def exchange_code(
    settings: OidcSettings,
    directory: ProviderDirectory,
    attempt: LoginAttempt,
    code: str,
) -> str:
    """The id_token for ``code``; the provider's access token is discarded.

    Notes never calls the provider again after this, so its access and
    refresh tokens are neither needed nor kept (Authentik's are ~2,300
    characters, too long for ``oauth_account.access_token`` anyway).
    """
    provider = await directory.metadata()
    form = {
        "grant_type": "authorization_code",
        "code": code,
        "redirect_uri": settings.redirect_uri,
        "code_verifier": attempt.code_verifier,
    }
    try:
        async with directory.http_client() as client:
            response = await client.post(
                provider.token_endpoint,
                data=form,
                # client_secret_basic, the OIDC default (Authentik accepts it).
                auth=(settings.client_id, settings.client_secret),
                headers={"Accept": "application/json"},
            )
    except httpx.HTTPError as exc:
        raise OidcLoginError(
            EXCHANGE_FAILED, f"token endpoint unreachable: {exc}"
        ) from exc
    if response.status_code >= 400:
        # The body names the OAuth error (invalid_grant and the like); it
        # never contains a token, so it is safe to log.
        logger.warning(
            "OIDC code exchange refused: %s %s",
            response.status_code,
            response.text[:300],
        )
        raise OidcLoginError(EXCHANGE_FAILED, "token endpoint refused the code")
    try:
        id_token = response.json().get("id_token")
    except ValueError as exc:
        raise OidcLoginError(EXCHANGE_FAILED, "token endpoint sent no JSON") from exc
    if not id_token:
        raise OidcLoginError(EXCHANGE_FAILED, "token endpoint sent no id_token")
    return id_token
