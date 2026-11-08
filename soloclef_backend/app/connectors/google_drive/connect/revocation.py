"""Revoking a stored Google grant, so a removed Drive connection is dead at Google too.

Revoking the refresh token ends the whole grant (its access tokens with it).
"""

from __future__ import annotations

import logging
from typing import Any

import httpx

from app.config import config
from app.utils.oauth_security import TokenEncryption

from .google_endpoints import revoke_uri

logger = logging.getLogger(__name__)


def _stored_token(connector_config: dict[str, Any] | None) -> str | None:
    cfg = connector_config or {}
    token = cfg.get("refresh_token") or cfg.get("token")
    if not token:
        return None
    if cfg.get("_token_encrypted"):
        if not config.SECRET_KEY:
            return None
        try:
            return TokenEncryption(config.SECRET_KEY).decrypt_token(token)
        except Exception:
            logger.warning("Could not decrypt a stored Google token to revoke it")
            return None
    return token


async def revoke_stored_grant(
    connector_config: dict[str, Any] | None,
    *,
    transport: httpx.AsyncBaseTransport | None = None,
) -> bool:
    """Best effort: False when there was nothing to revoke or Google did not confirm."""
    token = _stored_token(connector_config)
    if token is None:
        return False
    try:
        async with httpx.AsyncClient(timeout=10, transport=transport) as client:
            response = await client.post(
                revoke_uri(),
                data={"token": token},
                headers={"Content-Type": "application/x-www-form-urlencoded"},
            )
    except httpx.HTTPError as exc:
        logger.warning("Revoking a Google grant failed: %s", type(exc).__name__)
        return False
    # 400 invalid_token: already revoked or expired, which is the goal.
    return response.status_code == 200
