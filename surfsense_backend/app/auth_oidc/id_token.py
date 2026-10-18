"""Check the id_token is the provider's, for Notes, for this sign-in."""

from __future__ import annotations

from dataclasses import dataclass

import jwt

from app.auth_oidc.discovery import ProviderDirectory
from app.auth_oidc.errors import INVALID_ID_TOKEN, OidcLoginError
from app.auth_oidc.login_attempt import LoginAttempt
from app.auth_oidc.settings import OidcSettings

# Signatures made with a public/private key pair only. "none" and the HMAC
# family are never accepted, whatever the discovery document offers.
ACCEPTED_ALGORITHMS = frozenset({"RS256", "RS384", "RS512", "PS256", "ES256", "ES384"})
# Clock difference tolerated between this server and the provider.
LEEWAY_SECONDS = 60


@dataclass(frozen=True)
class VerifiedIdentity:
    subject: str
    email: str
    email_verified: bool
    name: str


def _key_for(jwks: dict, kid: str | None):
    keys = jwt.PyJWKSet.from_dict(jwks).keys
    for key in keys:
        if kid is None or key.key_id == kid:
            return key
    return None


async def verify_id_token(
    token: str,
    settings: OidcSettings,
    directory: ProviderDirectory,
    attempt: LoginAttempt,
) -> VerifiedIdentity:
    provider = await directory.metadata()
    try:
        header = jwt.get_unverified_header(token)
    except jwt.PyJWTError as exc:
        raise OidcLoginError(INVALID_ID_TOKEN, "id_token is not a JWT") from exc

    algorithm = header.get("alg")
    allowed = ACCEPTED_ALGORITHMS.intersection(provider.signing_algorithms)
    if algorithm not in allowed:
        raise OidcLoginError(INVALID_ID_TOKEN, f"id_token signed with {algorithm}")

    try:
        key = _key_for(await directory.signing_keys(), header.get("kid"))
        if key is None:  # rotated since the keys were last read
            key = _key_for(
                await directory.signing_keys(refresh=True), header.get("kid")
            )
    except jwt.PyJWTError as exc:
        raise OidcLoginError(INVALID_ID_TOKEN, "provider keys unreadable") from exc
    if key is None:
        raise OidcLoginError(INVALID_ID_TOKEN, "id_token signed with an unknown key")

    try:
        claims = jwt.decode(
            token,
            key.key,
            algorithms=[algorithm],
            audience=settings.client_id,
            issuer=provider.issuer,
            leeway=LEEWAY_SECONDS,
            options={"require": ["exp", "iat", "iss", "aud", "sub"]},
        )
    except jwt.PyJWTError as exc:
        raise OidcLoginError(INVALID_ID_TOKEN, f"id_token rejected: {exc}") from exc

    # OIDC Core 3.1.3.7: with several audiences, azp must be this client.
    audiences = claims["aud"] if isinstance(claims["aud"], list) else [claims["aud"]]
    if len(audiences) > 1 and claims.get("azp") != settings.client_id:
        raise OidcLoginError(INVALID_ID_TOKEN, "id_token is for another client")
    if claims.get("nonce") != attempt.nonce:
        raise OidcLoginError(INVALID_ID_TOKEN, "id_token nonce is not this sign-in's")

    return VerifiedIdentity(
        subject=str(claims["sub"]),
        email=str(claims.get("email") or "").strip().lower(),
        email_verified=claims.get("email_verified") is True,
        name=str(claims.get("name") or "").strip(),
    )
