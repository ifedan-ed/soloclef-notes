"""Check an Authentik access token is genuine, current and meant for agents.

Authentik's access tokens are JWTs signed with the provider's signing key, with
``iss`` the provider's issuer and ``aud`` (and ``azp``) its client id. A token
passes only when all of these hold; Authentik ignores RFC 8707 resource
indicators, so the audience check is what keeps a token issued to any other
application from being used here. Whether the person may use Notes at all is
read from the token too (``NOTES_ACCESS_CLAIM``), and decided in
``app.agent_sign_in.principal``.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

import jwt

from app.agent_sign_in.settings import AgentSignInSettings
from app.auth_oidc.discovery import ProviderDirectory
from app.auth_oidc.errors import OidcLoginError

# Signatures made with a public/private key pair only, as for the web sign-in:
# "none" and the HMAC family are never accepted, whatever discovery says.
ACCEPTED_ALGORITHMS = frozenset({"RS256", "RS384", "RS512", "PS256", "ES256", "ES384"})
# Clock difference tolerated between this server and Authentik.
LEEWAY_SECONDS = 60
# A token naming a key id Notes has not seen makes it re-read Authentik's keys
# (a key rotation needs no restart), but at most once a minute, so a stream of
# forged tokens with made-up key ids cannot turn into a stream of requests to
# the provider.
KEY_REFRESH_COOLDOWN_SECONDS = 60
# Added to every agent token by the "Soloclef Notes access (agents)" scope
# mapping (custom/authentik/soloclef-mcp.yaml, scope ``openid``): whether
# Authentik lets the person open the notes application at the moment the
# token is made, by that application's own bindings. Authentik makes a new
# token at every connect and every refresh, and its refresh grant never
# re-checks an application's access policy itself, so this claim is how
# taking someone's Notes access away reaches a connected agent.
NOTES_ACCESS_CLAIM = "soloclef_notes_access"


class AgentTokenRefusedError(Exception):
    """The token is not a valid agent sign-in; the reason is for the logs."""


class AgentProviderUnavailableError(Exception):
    """Authentik's metadata or keys could not be read, so the token could not
    be checked either way. Answered with 503, never 401: a 401 invalid_token
    would make Claude and ChatGPT drop a token that may be perfectly good and
    ask the person to sign in again, for every connected agent at once."""


@dataclass(frozen=True)
class AgentIdentity:
    subject: str
    email: str
    email_verified: bool
    # The token's ``NOTES_ACCESS_CLAIM``: True or False as Authentik decided
    # it, None when the token has no such claim or it is not a JSON boolean
    # (the mapping is missing or failed, or the openid scope was not granted).
    notes_access: bool | None = None


def _notes_access_of(claims: dict) -> bool | None:
    value = claims.get(NOTES_ACCESS_CLAIM)
    return value if isinstance(value, bool) else None


def unverified_issuer(token: str) -> str | None:
    """The ``iss`` a JWT claims, unchecked: only to tell which verifier to use."""
    try:
        claims = jwt.decode(token, options={"verify_signature": False})
    except jwt.PyJWTError:
        return None
    issuer = claims.get("iss")
    return issuer if isinstance(issuer, str) else None


def _key_for(jwks: dict, kid: str | None):
    for key in jwt.PyJWKSet.from_dict(jwks).keys:
        if kid is None or key.key_id == kid:
            return key
    return None


class AgentTokenVerifier:
    """One provider's keys and the rules above; shared by every request."""

    def __init__(
        self,
        settings: AgentSignInSettings,
        directory: ProviderDirectory | None = None,
        *,
        clock=time.monotonic,
    ) -> None:
        self._settings = settings
        self._directory = directory or ProviderDirectory(settings.discovery_url)
        self._clock = clock
        self._last_forced_refresh: float | None = None

    def claims_this_issuer(self, token: str) -> bool:
        """Whether the token says it is from the agent provider (unchecked)."""
        issuer = unverified_issuer(token)
        return issuer is not None and issuer.rstrip(
            "/"
        ) == self._settings.issuer.rstrip("/")

    async def _signing_key(self, kid: str | None):
        key = _key_for(await self._directory.signing_keys(), kid)
        if key is not None:
            return key
        now = self._clock()
        if (
            self._last_forced_refresh is not None
            and now - self._last_forced_refresh < KEY_REFRESH_COOLDOWN_SECONDS
        ):
            return None
        self._last_forced_refresh = now
        return _key_for(await self._directory.signing_keys(refresh=True), kid)

    async def verify(self, token: str) -> AgentIdentity:
        try:
            provider = await self._directory.metadata()
        except OidcLoginError as exc:
            raise AgentProviderUnavailableError(f"provider unavailable: {exc}") from exc
        try:
            header = jwt.get_unverified_header(token)
        except jwt.PyJWTError as exc:
            raise AgentTokenRefusedError("not a JWT") from exc

        algorithm = header.get("alg")
        if algorithm not in ACCEPTED_ALGORITHMS.intersection(
            provider.signing_algorithms
        ):
            raise AgentTokenRefusedError(f"signed with {algorithm}")

        try:
            key = await self._signing_key(header.get("kid"))
        except (OidcLoginError, jwt.PyJWTError) as exc:
            # Unreachable, or a key set Notes cannot read: the provider's side.
            raise AgentProviderUnavailableError("provider keys unreadable") from exc
        if key is None:
            raise AgentTokenRefusedError("signed with an unknown key")

        client_id = self._settings.client_id
        try:
            claims = jwt.decode(
                token,
                key.key,
                algorithms=[algorithm],
                audience=client_id,
                issuer=provider.issuer,
                leeway=LEEWAY_SECONDS,
                options={"require": ["exp", "iat", "iss", "aud", "sub"]},
            )
        except jwt.PyJWTError as exc:
            raise AgentTokenRefusedError(f"rejected: {exc}") from exc

        # Authentik always names the client the token was issued to in azp;
        # with several audiences it must be the agent client (OIDC Core 3.1.3.7).
        audiences = (
            claims["aud"] if isinstance(claims["aud"], list) else [claims["aud"]]
        )
        azp = claims.get("azp")
        if (azp is not None or len(audiences) > 1) and azp != client_id:
            raise AgentTokenRefusedError("issued to another client")

        return AgentIdentity(
            subject=str(claims["sub"]),
            email=str(claims.get("email") or "").strip().lower(),
            email_verified=claims.get("email_verified") is True,
            notes_access=_notes_access_of(claims),
        )
