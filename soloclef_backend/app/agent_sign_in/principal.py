"""From a bearer token to the Notes user an agent acts as, or a refusal.

``app.users.get_auth_context`` asks ``is_agent_token`` first: only a JWT that
claims the agent provider as its issuer takes this path, and from then on it
either becomes an agent principal or the request is refused. It never falls
through to the session check.
"""

from __future__ import annotations

import logging
from functools import cache

from fastapi import HTTPException, status
from sqlalchemy.ext.asyncio import AsyncSession

from app.agent_sign_in.access_token import (
    NOTES_ACCESS_CLAIM,
    AgentProviderUnavailableError,
    AgentTokenRefusedError,
    AgentTokenVerifier,
)
from app.agent_sign_in.account import NoAccountForAgentError, account_for_agent
from app.agent_sign_in.settings import AgentSignInSettings, load_agent_sign_in_settings
from app.brand import BRAND_NAME
from app.db import User

logger = logging.getLogger(__name__)


@cache
def agent_settings() -> AgentSignInSettings:
    return load_agent_sign_in_settings()


@cache
def _default_verifier() -> AgentTokenVerifier | None:
    settings = agent_settings()
    return AgentTokenVerifier(settings) if settings.enabled else None


def is_agent_token(token: str, verifier: AgentTokenVerifier | None = None) -> bool:
    verifier = verifier or _default_verifier()
    return verifier is not None and verifier.claims_this_issuer(token)


async def agent_user(
    token: str,
    session: AsyncSession,
    *,
    verifier: AgentTokenVerifier | None = None,
    provider_name: str | None = None,
) -> User:
    """The account the token acts as; raises 401 (bad token), 403 (Authentik
    does not let the person use Notes, or no account) or 503 (Authentik's
    metadata or keys unreadable, so the token could not be checked).

    Every Notes account Authentik lets into Notes may connect; Notes adds no
    group or domain rule of its own. Authentik's answer is the token's
    ``NOTES_ACCESS_CLAIM``, made again at every refresh, so a person whose
    Notes access is taken away is refused from their agent's next refresh
    (within an hour)."""
    verifier = verifier or _default_verifier()
    if verifier is None:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, detail="Unauthorized")
    try:
        identity = await verifier.verify(token)
    except AgentProviderUnavailableError as exc:
        logger.warning("Agent sign-in could not be checked: %s", exc)
        raise HTTPException(
            status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Single sign-on cannot be reached right now; try again shortly",
        ) from exc
    except AgentTokenRefusedError as exc:
        logger.info("Agent sign-in token refused: %s", exc)
        raise HTTPException(
            status.HTTP_401_UNAUTHORIZED,
            detail="The agent's sign-in is not valid or has expired",
            headers={"WWW-Authenticate": 'Bearer error="invalid_token"'},
        ) from exc
    if identity.notes_access is not True:
        # 403, not 401: signing in again as the same person cannot fix it, so
        # the client must not be sent back into a sign-in loop. A token
        # without the claim is refused too (fail closed): the provider was
        # set up without the blueprint's access mapping, or it failed.
        logger.info(
            "Agent sign-in for subject %s refused: %s",
            identity.subject,
            "Authentik does not let this person use Notes"
            if identity.notes_access is False
            else f"token has no {NOTES_ACCESS_CLAIM} claim",
        )
        raise HTTPException(
            status.HTTP_403_FORBIDDEN,
            detail=f"This sign-in may not use {BRAND_NAME}",
        )
    try:
        return await account_for_agent(
            identity,
            provider_name=provider_name or agent_settings().provider_name,
            session=session,
        )
    except NoAccountForAgentError as exc:
        logger.info("Agent sign-in for subject %s: %s", identity.subject, exc)
        raise HTTPException(
            status.HTTP_403_FORBIDDEN,
            detail=f"No {BRAND_NAME} account matches this sign-in",
        ) from exc
