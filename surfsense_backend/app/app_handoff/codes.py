"""Hand-off codes and page tickets: random, stored hashed, spent once.

A **code** (``CODE_LIFETIME_SECONDS``, 60 s) is what the app puts in the
browser sheet's address; the page's GET spends it. A **ticket**
(``TICKET_LIFETIME_SECONDS``, 10 minutes: long enough to pick files in the
Picker) is what the Drive Picker's page carries in its body to make its one
import. A **completion** (``COMPLETION_LIFETIME_SECONDS``, 5 minutes) is what
a Microsoft mail or Google Drive connect sends back to the app instead of
saving the mailbox or the Drive: it is saved only when the app that asked for
the hand-off spends it with its own bearer token and PKCE verifier
(``routes.complete_handoff``).
All three stand for a ``HandoffGrant``: who, for what, where.

Shared by every worker through Redis (``SET … EX NX``, ``GETDEL``); a Redis
that cannot be reached falls back to this process's memory with a warning
(as the app sign-in codes and the rate limiter do), so a hand-off then only
works when one worker serves both requests.

Pure logic, no FastAPI: tested without a server. ``MemoryStore`` and
``RedisStore`` take a lifetime and a key prefix; ``MemoryHandoffCodes`` and
``RedisHandoffCodes`` are the same with the code's 60 s (the routes' code
store, which a test overrides through ``routes.get_handoff_codes``).
"""

from __future__ import annotations

import hashlib
import json
import logging
import secrets
import time
from collections.abc import Callable
from dataclasses import asdict, dataclass
from typing import Protocol

logger = logging.getLogger(__name__)

#: How long the browser sheet has to open the code's page: 60 s, single use
#: (plan §4.3). Documented in custom/README.md ("The phone app's browser hand-off").
CODE_LIFETIME_SECONDS = 60
#: How long the Picker page has to make its one import: 10 minutes.
TICKET_LIFETIME_SECONDS = 600
#: How long the app has to confirm a Microsoft mailbox or a Google Drive its
#: sheet connected: 5 minutes (it does so as soon as the sheet comes back).
COMPLETION_LIFETIME_SECONDS = 300

#: Google's Picker for this person's Drive, importing into a notebook (B6).
DRIVE_PICKER = "drive_picker"
#: Connect, or reconnect, Google Drive (B10).
DRIVE_CONNECT = "drive_connect"
#: Connect, or reconnect, a Microsoft mailbox (B10).
MICROSOFT_MAIL = "microsoft_mail"
#: Everything a hand-off can start.
PURPOSES = (DRIVE_PICKER, DRIVE_CONNECT, MICROSOFT_MAIL)
#: The hand-offs whose result is saved only when the app confirms it with
#: the verifier behind the ``code_challenge`` it asked with: a code opened in
#: anyone else's browser can then link nothing to the account that made it.
#: Google Drive joined on 2026-10-11 (before, its link saved the opener's
#: Drive into the account that asked for it).
CONFIRMED_PURPOSES = frozenset({MICROSOFT_MAIL, DRIVE_CONNECT})

CODE_PREFIX = "surfsense:app_handoff:code"
TICKET_PREFIX = "surfsense:app_handoff:ticket"
COMPLETION_PREFIX = "surfsense:app_handoff:completion"


@dataclass(frozen=True)
class HandoffGrant:
    """What a code (or a page's ticket) stands for: whose flow, which one, from where."""

    purpose: str
    user_id: str
    workspace_id: int
    issued_at: float
    #: The Drive Picker: the notebook the picked files are added to; none: the library only.
    notebook_id: int | None = None
    #: Microsoft mail: the mailbox being reconnected (its address is preselected).
    account_id: str | None = None
    #: The Drive Picker's ticket: the Drive connection the page imports through.
    connector_id: int | None = None
    #: A confirmed purpose (``CONFIRMED_PURPOSES``): the app's S256 PKCE
    #: challenge, carried from the code to the completion.
    challenge: str | None = None
    #: A completion: what the provider granted, encrypted with SECRET_KEY,
    #: saved only when the app confirms it.
    sealed: str | None = None


class HandoffCodes(Protocol):
    async def issue(self, grant: HandoffGrant) -> str: ...

    async def redeem(self, secret: str) -> HandoffGrant | None: ...


def _key(secret: str) -> str:
    # Stored hashed: a dump of the store names nothing spendable.
    return hashlib.sha256(secret.encode("utf-8")).hexdigest()


def new_secret() -> str:
    return secrets.token_urlsafe(32)


#: The connectors track's name for it.
new_code = new_secret


def _fresh(grant: HandoffGrant, now: float, lifetime: int) -> bool:
    return grant.issued_at + lifetime > now


class MemoryStore:
    """One process's codes or tickets: the fallback when Redis is down, and tests."""

    def __init__(self, lifetime: int, clock: Callable[[], float] = time.time) -> None:
        self.lifetime = lifetime
        self._clock = clock
        self._items: dict[str, HandoffGrant] = {}

    def _sweep(self) -> None:
        now = self._clock()
        for key, grant in list(self._items.items()):
            if not _fresh(grant, now, self.lifetime):
                del self._items[key]

    def keep(self, secret: str, grant: HandoffGrant) -> None:
        self._sweep()
        self._items[_key(secret)] = grant

    async def issue(self, grant: HandoffGrant) -> str:
        secret = new_secret()
        self.keep(secret, grant)
        return secret

    async def redeem(self, secret: str) -> HandoffGrant | None:
        """The grant behind ``secret``, once: a second redeem finds nothing."""
        self._sweep()
        if not isinstance(secret, str) or not secret:
            return None
        return self._items.pop(_key(secret), None)


class RedisStore:
    """Codes or tickets shared by every worker through Redis."""

    def __init__(
        self,
        url: str,
        prefix: str,
        lifetime: int,
        clock: Callable[[], float] = time.time,
        client=None,
    ) -> None:
        self._url = url
        self._prefix = prefix
        self.lifetime = lifetime
        self._clock = clock
        self._fallback = MemoryStore(lifetime, clock)
        self._client = client

    def _redis(self):
        if self._client is None:
            import redis.asyncio as aioredis

            self._client = aioredis.from_url(self._url, decode_responses=True)
        return self._client

    async def issue(self, grant: HandoffGrant) -> str:
        secret = new_secret()
        payload = json.dumps(asdict(grant))
        try:
            stored = await self._redis().set(
                f"{self._prefix}:{_key(secret)}", payload, ex=self.lifetime, nx=True
            )
            if stored:
                return secret
        except Exception as exc:
            logger.warning(
                "app hand-off: Redis unavailable (%s); using this worker's memory",
                type(exc).__name__,
            )
        self._fallback.keep(secret, grant)
        return secret

    async def redeem(self, secret: str) -> HandoffGrant | None:
        if not isinstance(secret, str) or not secret:
            return None
        try:
            payload = await self._redis().getdel(f"{self._prefix}:{_key(secret)}")
        except Exception as exc:
            logger.warning(
                "app hand-off: Redis unavailable (%s); reading this worker's memory",
                type(exc).__name__,
            )
            payload = None
        if payload is None:
            return await self._fallback.redeem(secret)
        try:
            grant = HandoffGrant(**json.loads(payload))
        except (ValueError, TypeError):
            return None
        return grant if _fresh(grant, self._clock(), self.lifetime) else None


class MemoryHandoffCodes(MemoryStore):
    """One process's 60 s codes (tests, and the fallback)."""

    def __init__(
        self,
        clock: Callable[[], float] = time.time,
        lifetime: int = CODE_LIFETIME_SECONDS,
    ) -> None:
        super().__init__(lifetime, clock)

    @property
    def _codes(self) -> dict[str, HandoffGrant]:
        """The hashed codes held (the connectors track's tests read them by this name)."""
        return self._items


class RedisHandoffCodes(RedisStore):
    """The 60 s codes every worker shares through Redis."""

    def __init__(
        self,
        url: str,
        *,
        lifetime: int = CODE_LIFETIME_SECONDS,
        clock: Callable[[], float] = time.time,
        client=None,
    ) -> None:
        super().__init__(url, CODE_PREFIX, lifetime, clock, client)


_codes: HandoffCodes | None = None
_tickets: HandoffCodes | None = None
_completions: HandoffCodes | None = None


def code_store() -> HandoffCodes:
    """The codes, through Redis (a test sets ``_codes`` to a ``MemoryStore``)."""
    global _codes
    if _codes is None:
        from app.config import config

        _codes = RedisHandoffCodes(config.REDIS_APP_URL)
    return _codes


def ticket_store() -> HandoffCodes:
    """The Picker pages' tickets, through Redis (a test sets ``_tickets``)."""
    global _tickets
    if _tickets is None:
        from app.config import config

        _tickets = RedisStore(
            config.REDIS_APP_URL, TICKET_PREFIX, TICKET_LIFETIME_SECONDS
        )
    return _tickets


def completion_store() -> HandoffCodes:
    """The completions waiting for the app, through Redis (a test sets ``_completions``).

    Also the FastAPI dependency of the Microsoft callback and of
    ``POST /app/handoff/complete``, so a test can override it there.
    """
    global _completions
    if _completions is None:
        from app.config import config

        _completions = RedisStore(
            config.REDIS_APP_URL, COMPLETION_PREFIX, COMPLETION_LIFETIME_SECONDS
        )
    return _completions
