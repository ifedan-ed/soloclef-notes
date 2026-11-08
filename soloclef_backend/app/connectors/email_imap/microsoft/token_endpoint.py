"""Microsoft's token endpoint: a consent code for tokens, and refreshes.

Two failures are told apart because they need different fixes. A grant
Microsoft no longer honours (consent removed, sign-in sessions revoked,
expired after 90 days unused, sign-in policy now wants the person) is
:class:`GrantRevokedError`: only the mailbox owner can fix it, by
reconnecting. A password change alone is not one of them: this app has a
client secret, and Microsoft keeps a confidential client's refresh tokens
alive through it (custom/docs/email-imap-connector.md). Anything else (Microsoft
unreachable or erroring, or this server's client secret expired or wrong) is
:class:`TokenEndpointError` and leaves the mailbox's connection alone.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

import httpx

from .settings import (
    IMAP_SCOPE,
    SCOPES,
    TOKEN_TIMEOUT_SECONDS,
    MicrosoftMailSettings,
)

logger = logging.getLogger(__name__)

# OAuth errors that mean "this grant is dead; the person must sign in again".
_NEEDS_THE_PERSON = frozenset(
    {"invalid_grant", "interaction_required", "consent_required", "login_required"}
)
_IMAP_PERMISSION = IMAP_SCOPE.rsplit("/", 1)[-1].lower()


class TokenEndpointError(Exception):
    """Microsoft could not be asked, or refused this server (not the grant)."""


class GrantRevokedError(TokenEndpointError):
    """Microsoft refused the grant itself: the mailbox must be reconnected."""


@dataclass(frozen=True)
class TokenGrant:
    access_token: str
    #: None when Microsoft kept the previous refresh token.
    refresh_token: str | None
    expires_at: datetime
    scopes: frozenset[str]
    id_token: str | None = None

    def __repr__(self) -> str:  # tokens never reach a log or a traceback
        return f"TokenGrant(expires_at={self.expires_at.isoformat()}, scopes={sorted(self.scopes)})"

    @property
    def grants_imap(self) -> bool:
        """Whether the person left IMAP access ticked on the consent page."""
        return any(
            scope.rsplit("/", 1)[-1].lower() == _IMAP_PERMISSION
            for scope in self.scopes
        )


async def redeem_code(
    settings: MicrosoftMailSettings,
    code: str,
    code_verifier: str,
    *,
    transport: httpx.AsyncBaseTransport | None = None,
    now: datetime | None = None,
) -> TokenGrant:
    """The tokens for the one-time code Microsoft's consent page sent back."""
    return await _ask(
        settings,
        {
            "grant_type": "authorization_code",
            "code": code,
            "code_verifier": code_verifier,
            "redirect_uri": settings.redirect_uri,
        },
        transport=transport,
        now=now,
    )


async def refresh_grant(
    settings: MicrosoftMailSettings,
    refresh_token: str,
    *,
    transport: httpx.AsyncBaseTransport | None = None,
    now: datetime | None = None,
) -> TokenGrant:
    """A new access token (and usually a new refresh token) for a stored grant."""
    return await _ask(
        settings,
        {"grant_type": "refresh_token", "refresh_token": refresh_token},
        transport=transport,
        now=now,
    )


async def _ask(
    settings: MicrosoftMailSettings,
    form: dict[str, str],
    *,
    transport: httpx.AsyncBaseTransport | None,
    now: datetime | None,
) -> TokenGrant:
    body = {
        **form,
        "client_id": settings.client_id,
        # client_secret_post: what Microsoft's own samples for web apps use.
        "client_secret": settings.client_secret,
        "scope": " ".join(SCOPES),
    }
    try:
        async with httpx.AsyncClient(
            timeout=TOKEN_TIMEOUT_SECONDS, transport=transport
        ) as client:
            response = await client.post(
                settings.token_url, data=body, headers={"Accept": "application/json"}
            )
    except httpx.HTTPError as exc:
        raise TokenEndpointError(
            f"Microsoft's token endpoint was unreachable ({type(exc).__name__})"
        ) from exc
    try:
        payload = response.json()
    except ValueError:
        payload = {}
    if not isinstance(payload, dict):
        payload = {}
    if response.status_code >= 400:
        error = str(payload.get("error") or f"http_{response.status_code}")
        codes = payload.get("error_codes") or []
        # The error name and AADSTS number say why; neither carries a token.
        logger.warning(
            "Microsoft token endpoint refused %s: %s %s",
            form["grant_type"],
            error,
            codes[:1],
        )
        if error in _NEEDS_THE_PERSON:
            raise GrantRevokedError(error)
        raise TokenEndpointError(error)
    access_token = payload.get("access_token")
    if not isinstance(access_token, str) or not access_token:
        raise TokenEndpointError("Microsoft's token endpoint sent no access token")
    try:
        lifetime = int(payload.get("expires_in") or 0)
    except (TypeError, ValueError):
        lifetime = 0
    refresh_token = payload.get("refresh_token")
    id_token = payload.get("id_token")
    return TokenGrant(
        access_token=access_token,
        refresh_token=refresh_token if isinstance(refresh_token, str) else None,
        expires_at=(now or datetime.now(UTC)) + timedelta(seconds=max(lifetime, 0)),
        scopes=frozenset(str(payload.get("scope") or "").split()),
        id_token=id_token if isinstance(id_token, str) else None,
    )
