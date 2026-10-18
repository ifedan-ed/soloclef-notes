"""The phone app's sign-in through Example SSO (plan item B1).

The app cannot read the cookies a web sign-in ends with, so it signs in
through the same provider in the phone's browser sheet and asks for a
different ending::

    GET /api/v1/auth/oidc/app/start?app_redirect=soloclef://auth
        &challenge=<S256>&challenge_method=S256&device=<label>[&choose_account=1]

The callback then sends the sheet back to the app's own address with a
one-time code instead of setting cookies, and the app swaps the code for its
session at ``POST /api/v1/auth/oidc/app/exchange``, proving with the PKCE
verifier behind the challenge (RFC 7636, S256) that it is the app that asked.
Another app on the phone that catches the redirect holds a code it cannot
spend. (PedsHub's ``src/utils/appSignIn.js`` is the pattern.)

What this module holds is the part with no FastAPI in it, so it is tested
without a server: the request's checks, the challenge arithmetic, the
one-time codes (``CODE_LIFETIME_SECONDS``, spent once, stored hashed) and
the app's return addresses. ``app_routes.py`` is the HTTP shell around it.
"""

from __future__ import annotations

import base64
import hashlib
import json
import logging
import re
import secrets
import time
from collections.abc import Callable, Mapping
from dataclasses import asdict, dataclass
from typing import Protocol
from urllib.parse import urlencode

logger = logging.getLogger(__name__)

# How long the app has to spend its code after the sheet comes back: 60 s,
# single use. The app says "Sign-in took too long. Try again." past it.
CODE_LIFETIME_SECONDS = 60

# base64url(sha256(...)) is exactly 43 characters; a verifier is 43-128
# characters of RFC 7636's unreserved set.
_CHALLENGE = re.compile(r"^[A-Za-z0-9_-]{43}$")
_VERIFIER = re.compile(r"^[A-Za-z0-9._~-]{43,128}$")
# A device's label in the signed-in devices list ("iPhone 15 Pro · Soloclef app").
DEVICE_LABEL_MAX = 100

# What the app is told when its sign-in cannot start or finish (the app's
# src/features/auth/reasons.ts words each one).
BAD_REDIRECT = "APP_REDIRECT_NOT_ALLOWED"
BAD_CHALLENGE = "APP_BAD_CHALLENGE"
CODE_EXPIRED = "APP_CODE_EXPIRED"
WRONG_VERIFIER = "APP_WRONG_VERIFIER"


class AppSignInRefused(Exception):  # noqa: N818 - the app reads it as a refusal with a code, not an error
    """An app's request that cannot be served, with the code the app shows."""

    def __init__(self, code: str, reason: str = "") -> None:
        super().__init__(reason or code)
        self.code = code


@dataclass(frozen=True)
class AppRequest:
    """What an app sign-in carries from its start to the exchange."""

    redirect: str
    challenge: str
    device: str
    choose_account: bool = False


def parse_app_request(params: Mapping[str, str], allowed: frozenset[str]) -> AppRequest:
    """The request behind ``/app/start``'s query, or why it is refused.

    ``allowed`` is the allow-list of return addresses (``OIDC_APP_REDIRECTS``):
    only an address on it is ever redirected to, so a forged start cannot send
    a code anywhere else.
    """
    redirect = (params.get("app_redirect") or "").strip()
    if redirect not in allowed:
        raise AppSignInRefused(BAD_REDIRECT, "app_redirect is not on the allow-list")
    challenge = (params.get("challenge") or "").strip()
    method = (params.get("challenge_method") or "S256").strip()
    if method != "S256" or not _CHALLENGE.match(challenge):
        raise AppSignInRefused(BAD_CHALLENGE, "challenge is not an S256 challenge")
    device = " ".join((params.get("device") or "").split())[:DEVICE_LABEL_MAX]
    choose = (params.get("choose_account") or "").strip().lower() in {
        "1",
        "true",
        "yes",
    }
    return AppRequest(
        redirect=redirect, challenge=challenge, device=device, choose_account=choose
    )


def challenge_of(verifier: str) -> str:
    """RFC 7636 S256: base64url(sha256(verifier)) without padding."""
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def verifier_matches(verifier: str, challenge: str) -> bool:
    """Whether ``verifier`` is the one behind ``challenge`` (constant time)."""
    if not isinstance(verifier, str) or not _VERIFIER.match(verifier):
        return False
    return secrets.compare_digest(challenge_of(verifier), challenge)


def app_return(
    redirect: str, *, code: str | None = None, error: str | None = None
) -> str:
    """Where the sheet is sent back: the app's address with a code, or an error."""
    query = {"code": code} if code else {"error": error or "OIDC_PROVIDER_ERROR"}
    return f"{redirect}?{urlencode(query)}"


@dataclass(frozen=True)
class AppGrant:
    """What a one-time code stands for: whose session, for which app request."""

    user_id: str
    challenge: str
    device: str
    issued_at: float


class AppCodeStore(Protocol):
    async def issue(self, grant: AppGrant) -> str: ...

    async def redeem(self, code: str) -> AppGrant | None: ...


def _key(code: str) -> str:
    # Stored hashed: a dump of the store names no spendable code.
    return hashlib.sha256(code.encode("ascii")).hexdigest()


def new_code() -> str:
    return secrets.token_urlsafe(32)


class MemoryAppCodes:
    """One process's codes: the fallback when Redis is unreachable, and tests."""

    def __init__(
        self,
        clock: Callable[[], float] = time.time,
        lifetime: int = CODE_LIFETIME_SECONDS,
    ) -> None:
        self._clock = clock
        self._lifetime = lifetime
        self._codes: dict[str, AppGrant] = {}

    def _sweep(self) -> None:
        now = self._clock()
        for key, grant in list(self._codes.items()):
            if grant.issued_at + self._lifetime <= now:
                del self._codes[key]

    async def issue(self, grant: AppGrant) -> str:
        self._sweep()
        code = new_code()
        self._codes[_key(code)] = grant
        return code

    async def redeem(self, code: str) -> AppGrant | None:
        """The grant behind ``code``, once: a second redeem finds nothing."""
        self._sweep()
        if not isinstance(code, str) or not code:
            return None
        return self._codes.pop(_key(code), None)


class RedisAppCodes:
    """Codes shared by every worker through Redis (``SET … EX NX`` and ``GETDEL``).

    A Redis that cannot be reached falls back to this process's memory, as the
    rate limiter does, with a warning: a sign-in then only works when the
    same worker serves the callback and the exchange.
    """

    def __init__(
        self,
        url: str,
        *,
        lifetime: int = CODE_LIFETIME_SECONDS,
        clock: Callable[[], float] = time.time,
    ) -> None:
        self._url = url
        self._lifetime = lifetime
        self._clock = clock
        self._fallback = MemoryAppCodes(clock, lifetime)
        self._client = None

    def _redis(self):
        if self._client is None:
            import redis.asyncio as aioredis

            self._client = aioredis.from_url(self._url, decode_responses=True)
        return self._client

    async def issue(self, grant: AppGrant) -> str:
        code = new_code()
        payload = json.dumps(asdict(grant))
        try:
            stored = await self._redis().set(
                f"surfsense:app_sign_in:{_key(code)}",
                payload,
                ex=self._lifetime,
                nx=True,
            )
            if stored:
                return code
        except Exception as exc:
            logger.warning(
                "app sign-in codes: Redis unavailable (%s); using this worker's memory",
                exc,
            )
        # NX refused (a hash collision, in theory) or Redis is down: this process keeps it.
        self._fallback._codes[_key(code)] = grant
        return code

    async def redeem(self, code: str) -> AppGrant | None:
        if not isinstance(code, str) or not code:
            return None
        try:
            payload = await self._redis().getdel(f"surfsense:app_sign_in:{_key(code)}")
        except Exception as exc:
            logger.warning(
                "app sign-in codes: Redis unavailable (%s); reading this worker's memory",
                exc,
            )
            payload = None
        if payload is None:
            return await self._fallback.redeem(code)
        try:
            data = json.loads(payload)
            grant = AppGrant(**data)
        except (ValueError, TypeError):
            return None
        if grant.issued_at + self._lifetime <= self._clock():
            return None
        return grant
