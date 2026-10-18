"""Ask the backend whether a bearer token is a valid agent sign-in.

The backend is the one judge of a token (it checks the Authentik signature,
issuer, audience and expiry, then finds the Notes account): this server only
needs to know before a request reaches a tool, so that a bad or expired token
gets an HTTP 401 the client can act on (refresh, or sign in again) instead of
an error inside a tool result. It asks ``GET /verify-token`` once per token
and keeps the answer for at most a minute, or until the token expires if that
is sooner. The backend still checks the token on every call a tool makes.
"""

from __future__ import annotations

import base64
import hashlib
import json
import time
from collections import OrderedDict
from dataclasses import dataclass

import httpx

# How long a "yes" is remembered (never past the token's own expiry). The
# backend still checks every call a tool makes; this only spares a second
# round trip per MCP request.
ACCEPT_CACHE_SECONDS = 60
# Distinct tokens remembered at once; the oldest is forgotten first.
MAX_CACHED_TOKENS = 4096
CHECK_TIMEOUT_SECONDS = 10.0
# The backend's principal kinds this server lets through on a sign-in token.
# API keys never come here, and a web session token is not for agents.
_AGENT_METHODS = frozenset({"agent"})


@dataclass(frozen=True)
class SignInVerdict:
    # 200 accepted; 401 invalid (only when the backend says 401); 403 no
    # account or not allowed; 503 could not be checked (backend unreachable or
    # any other answer, e.g. a 5xx while the provider is down).
    status: int
    identity: str | None = None
    message: str = ""

    @property
    def accepted(self) -> bool:
        return self.status == 200


def _unverified_expiry(token: str) -> float | None:
    """The ``exp`` a JWT carries, unchecked (the backend has checked it)."""
    try:
        payload = token.split(".")[1]
        payload += "=" * (-len(payload) % 4)
        exp = json.loads(base64.urlsafe_b64decode(payload)).get("exp")
    except (IndexError, ValueError, AttributeError):
        return None
    return float(exp) if isinstance(exp, int | float) else None


def _detail(response: httpx.Response) -> str:
    try:
        detail = response.json().get("detail")
    except (ValueError, AttributeError):
        return ""
    return detail if isinstance(detail, str) else ""


class BackendSignInCheck:
    def __init__(
        self,
        base_url: str,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        clock=time.monotonic,
        wall_clock=time.time,
    ) -> None:
        self._http = httpx.AsyncClient(
            base_url=base_url,
            timeout=CHECK_TIMEOUT_SECONDS,
            transport=transport,
            headers={"Accept": "application/json", "X-SurfSense-Client": "mcp"},
        )
        self._clock = clock
        self._wall_clock = wall_clock
        self._accepted: OrderedDict[str, tuple[float, str]] = OrderedDict()

    def _remembered(self, key: str) -> str | None:
        entry = self._accepted.get(key)
        if entry is None:
            return None
        until, identity = entry
        if self._clock() >= until:
            del self._accepted[key]
            return None
        return identity

    def _remember(self, key: str, token: str, identity: str) -> None:
        keep = float(ACCEPT_CACHE_SECONDS)
        expiry = _unverified_expiry(token)
        if expiry is not None:
            keep = min(keep, expiry - self._wall_clock())
        if keep <= 0:
            return
        self._accepted[key] = (self._clock() + keep, identity)
        self._accepted.move_to_end(key)
        while len(self._accepted) > MAX_CACHED_TOKENS:
            self._accepted.popitem(last=False)

    async def check(self, token: str) -> SignInVerdict:
        key = hashlib.sha256(token.encode()).hexdigest()
        identity = self._remembered(key)
        if identity is not None:
            return SignInVerdict(200, identity)
        try:
            response = await self._http.get(
                "/verify-token", headers={"Authorization": f"Bearer {token}"}
            )
        except httpx.HTTPError:
            return SignInVerdict(503, message="Could not reach Soloclef Notes")
        if response.status_code == 403:
            return SignInVerdict(403, message=_detail(response))
        if response.status_code == 401:
            return SignInVerdict(401, message=_detail(response))
        if response.status_code != 200:
            # The backend could not decide (a 5xx, or 503 when Authentik's
            # keys cannot be read): 503, never 401 invalid_token, which would
            # make the client drop a good token and ask the person to sign in
            # again.
            return SignInVerdict(
                503,
                message=_detail(response)
                or "Soloclef Notes could not check the sign-in; try again shortly",
            )
        try:
            body = response.json()
        except ValueError:
            return SignInVerdict(503, message="Unexpected answer from Soloclef Notes")
        if body.get("method") not in _AGENT_METHODS or not body.get("user_id"):
            return SignInVerdict(
                401, message="Sign in through Soloclef, or use an API key"
            )
        identity = f"user:{body['user_id']}"
        self._remember(key, token, identity)
        return SignInVerdict(200, identity)

    async def aclose(self) -> None:
        await self._http.aclose()
