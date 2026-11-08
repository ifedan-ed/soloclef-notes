"""IMAP sign-in settings for a stored Microsoft mailbox, with a token that works.

Access tokens last about an hour, so each use checks the stored one and,
when it has under ``REFRESH_MARGIN_SECONDS`` left, trades the refresh token
for a new pair and hands the encrypted result to ``save``. When Microsoft
refuses the grant itself the record is marked ``needs_reconnect`` and every
later use fails fast with the same message, without asking Microsoft again,
until the owner reconnects (the connector then shows "Reconnect").
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime, timedelta
from typing import Any

import httpx

from ..client import ImapAccountSettings, ImapConnectionError
from ..credentials import InvalidEmailAccountError
from .account_record import (
    NEEDS_RECONNECT,
    refreshed_fields,
    stored_access_token,
    stored_refresh_token,
)
from .settings import (
    IMAP_HOST,
    IMAP_PORT,
    REFRESH_MARGIN_SECONDS,
    MicrosoftMailSettings,
    current_settings,
)
from .token_endpoint import GrantRevokedError, TokenEndpointError, refresh_grant

logger = logging.getLogger(__name__)

RECONNECT_MESSAGE = (
    "Microsoft no longer accepts this mailbox's sign-in. "
    "Reconnect it in Connectors, Email (IMAP)."
)
UNREACHABLE_MESSAGE = (
    "Couldn't reach Microsoft to sign in to this mailbox. Try again shortly."
)
SERVER_REFUSED_MESSAGE = (
    "Microsoft refused this server's app credentials. "
    "The administrator needs to renew the Microsoft client secret."
)
NOT_SET_UP_MESSAGE = "Microsoft accounts aren't set up on this server."

# OAuth errors about this server's app registration, not about the mailbox.
_SERVER_ERRORS = frozenset({"invalid_client", "unauthorized_client"})

SaveFields = Callable[[dict[str, Any]], Awaitable[None]]


async def _save_quietly(save: SaveFields, fields: dict[str, Any]) -> None:
    # A failed save only costs a refresh next time; it must not fail the read.
    try:
        await save(fields)
    except Exception:
        logger.warning("Saving a Microsoft mailbox's token state failed", exc_info=True)


async def microsoft_imap_settings(
    account: dict[str, Any],
    *,
    save: SaveFields,
    settings: MicrosoftMailSettings | None = None,
    transport: httpx.AsyncBaseTransport | None = None,
    now: datetime | None = None,
) -> ImapAccountSettings:
    settings = settings or current_settings()
    if account.get(NEEDS_RECONNECT):
        raise ImapConnectionError(RECONNECT_MESSAGE)
    if not settings.configured:
        raise ImapConnectionError(NOT_SET_UP_MESSAGE)
    now = now or datetime.now(UTC)
    try:
        token, expires_at = stored_access_token(account, secret_key=settings.secret_key)
        refresh_token = stored_refresh_token(account, secret_key=settings.secret_key)
    except (ValueError, InvalidEmailAccountError):
        # Missing, or encrypted under a SECRET_KEY this server no longer has.
        await _save_quietly(save, {NEEDS_RECONNECT: True})
        raise ImapConnectionError(RECONNECT_MESSAGE) from None

    margin = timedelta(seconds=REFRESH_MARGIN_SECONDS)
    if token is None or expires_at is None or expires_at - now < margin:
        try:
            grant = await refresh_grant(
                settings, refresh_token, transport=transport, now=now
            )
        except GrantRevokedError:
            await _save_quietly(save, {NEEDS_RECONNECT: True})
            raise ImapConnectionError(RECONNECT_MESSAGE) from None
        except TokenEndpointError as exc:
            if str(exc) in _SERVER_ERRORS:
                raise ImapConnectionError(SERVER_REFUSED_MESSAGE) from exc
            raise ImapConnectionError(UNREACHABLE_MESSAGE) from exc
        await _save_quietly(
            save, refreshed_fields(grant, secret_key=settings.secret_key)
        )
        token = grant.access_token

    return ImapAccountSettings(
        host=IMAP_HOST,
        port=IMAP_PORT,
        use_tls=True,
        username=str(account.get("username") or ""),
        password="",
        oauth_access_token=token,
    )
