"""Cloudflare Turnstile: a human check on sign-in, registration and password reset.

The browser solves the widget and sends its one-time token in the
``X-Turnstile-Token`` header; the route verifies it with Cloudflare's
siteverify before doing anything else. Off while ``TURNSTILE_SECRET_KEY`` is
empty, so a deployment without keys behaves exactly as before.

Fails closed: an unreachable Cloudflare is a refusal (503), never a pass. The
secret and the token are never logged.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

import httpx
from fastapi import HTTPException, Request, status

from app.config import config

logger = logging.getLogger(__name__)

TOKEN_HEADER = "X-Turnstile-Token"
# Cloudflare's limit; anything longer is not a Turnstile token.
MAX_TOKEN_LENGTH = 2048

REQUIRED = "TURNSTILE_REQUIRED"
FAILED = "TURNSTILE_FAILED"
UNAVAILABLE = "TURNSTILE_UNAVAILABLE"


@dataclass(frozen=True)
class TurnstileResult:
    success: bool
    error_codes: list[str] = field(default_factory=list)
    # True when Cloudflare could not be asked or answered nonsense.
    unavailable: bool = False


def turnstile_enabled() -> bool:
    return bool(config.TURNSTILE_SECRET_KEY)


async def verify_turnstile_token(
    token: str,
    remote_ip: str | None = None,
    *,
    client: httpx.AsyncClient | None = None,
) -> TurnstileResult:
    """Ask Cloudflare whether ``token`` is a fresh, valid solve."""
    payload = {"secret": config.TURNSTILE_SECRET_KEY, "response": token}
    if remote_ip:
        payload["remoteip"] = remote_ip
    try:
        if client is None:
            async with httpx.AsyncClient(
                timeout=config.TURNSTILE_TIMEOUT_SECONDS
            ) as own:
                response = await own.post(config.TURNSTILE_VERIFY_URL, data=payload)
        else:
            response = await client.post(config.TURNSTILE_VERIFY_URL, data=payload)
        response.raise_for_status()
        body = response.json()
    except (httpx.HTTPError, ValueError) as exc:
        logger.warning("Turnstile verification unavailable: %s", type(exc).__name__)
        return TurnstileResult(success=False, unavailable=True)
    if not isinstance(body, dict):
        return TurnstileResult(success=False, unavailable=True)
    codes = [str(code) for code in body.get("error-codes") or []]
    return TurnstileResult(success=body.get("success") is True, error_codes=codes)


async def require_turnstile(request: Request) -> None:
    """Route dependency: refuse the request unless its Turnstile token checks out."""
    if not turnstile_enabled():
        return
    token = (request.headers.get(TOKEN_HEADER) or "").strip()
    if not token or len(token) > MAX_TOKEN_LENGTH:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=REQUIRED)

    from app.rate_limiter import get_real_client_ip

    result = await verify_turnstile_token(token, get_real_client_ip(request))
    if result.unavailable:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE, detail=UNAVAILABLE
        )
    if not result.success:
        logger.info(
            "Turnstile refused %s %s: %s",
            request.method,
            request.url.path,
            ",".join(result.error_codes) or "no reason",
        )
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=FAILED)
