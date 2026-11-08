"""Refresh tokens: one family per sign-in, rotated on every refresh.

Two kinds of family (``RefreshToken.kind``):

- ``web`` (a browser, the desktop app): a token lives
  ``REFRESH_TOKEN_LIFETIME_SECONDS`` from its last use (sliding) and the
  family ``REFRESH_ABSOLUTE_LIFETIME_SECONDS`` from the sign-in.
- ``app`` (the Soloclef phone app, plan decision 5): the family lives
  ``APP_SESSION_LIFETIME_SECONDS`` (365 days) from the sign-in and each token
  lives as long as the family, so a phone left in a drawer for a month is
  still signed in. The family carries the phone's ``device_label`` and is
  what the signed-in devices list (``list_devices``) shows; revoking a family
  (``revoke_family``) signs that one device out.

The lifetime arithmetic (``token_expiry``) and the grouping of rows into
devices (``devices_from_rows``) take no database, so tests cover them whole.

Ending a family ends its phone's pushes too: every revoke here deletes the
push tokens registered under the families it ends (``push_devices.session_family_id``,
``_forget_push_devices``), and ``app/push/store.devices_of`` skips any whose
family is no longer live (expired, or ended by token reuse), so a phone the
server signed out stops getting the account's pushes at once.
"""

import hashlib
import logging
import secrets
import uuid
from collections.abc import Iterable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

from sqlalchemy import delete, select, update

from app.config import config
from app.db import PushDevice, RefreshToken, async_session_maker

logger = logging.getLogger(__name__)

KIND_WEB = "web"
KIND_APP = "app"


@dataclass(frozen=True)
class RefreshRotationResult:
    user_id: uuid.UUID
    refresh_token: str | None
    access_only: bool = False
    family_id: uuid.UUID | None = None


@dataclass(frozen=True)
class IssuedRefresh:
    """A new family's first token, and the family it starts."""

    token: str
    family_id: uuid.UUID


@dataclass(frozen=True)
class DeviceSession:
    """One signed-in device: a token family with a live token."""

    family_id: uuid.UUID
    kind: str
    device_label: str | None
    signed_in_at: datetime
    last_used_at: datetime
    expires_at: datetime


def generate_refresh_token() -> str:
    """Generate a cryptographically secure refresh token."""
    return secrets.token_urlsafe(32)


def hash_token(token: str) -> str:
    """Hash a token for secure storage."""
    return hashlib.sha256(token.encode()).hexdigest()


async def _forget_push_devices(
    session, user_id: uuid.UUID, family_ids: Iterable[uuid.UUID] | None = None
) -> int:
    """The push tokens of these ended families (all of the user's when None) go with them."""
    where = [PushDevice.user_id == user_id]
    if family_ids is not None:
        families = list({f for f in family_ids if f is not None})
        if not families:
            return 0
        where.append(PushDevice.session_family_id.in_(families))
    result = await session.execute(delete(PushDevice).where(*where))
    return result.rowcount or 0


def family_lifetime_seconds(kind: str) -> int:
    """How long a family of this kind lives from its sign-in."""
    if kind == KIND_APP:
        return config.APP_SESSION_LIFETIME_SECONDS
    return config.REFRESH_ABSOLUTE_LIFETIME_SECONDS


def token_expiry(
    kind: str, now: datetime, family_cap: datetime, idle_seconds: int | None = None
) -> datetime:
    """When a token of this kind, made now, stops working.

    A web token slides: ``idle_seconds`` from now, never past the family's
    cap. An app token lives as long as its family: no idle timeout.
    """
    if kind == KIND_APP:
        return family_cap
    idle = (
        config.REFRESH_TOKEN_LIFETIME_SECONDS if idle_seconds is None else idle_seconds
    )
    return min(now + timedelta(seconds=idle), family_cap)


async def issue_refresh_token(
    user_id: uuid.UUID,
    *,
    kind: str = KIND_WEB,
    device_label: str | None = None,
    family_id: uuid.UUID | None = None,
    absolute_expiry: datetime | None = None,
) -> IssuedRefresh:
    """A new family (a sign-in) and its first token."""
    token = generate_refresh_token()
    now = datetime.now(UTC)
    if absolute_expiry is None:
        absolute_expiry = now + timedelta(seconds=family_lifetime_seconds(kind))
    if family_id is None:
        family_id = uuid.uuid4()
    async with async_session_maker() as session:
        session.add(
            RefreshToken(
                user_id=user_id,
                token_hash=hash_token(token),
                expires_at=token_expiry(kind, now, absolute_expiry),
                family_id=family_id,
                absolute_expiry=absolute_expiry,
                kind=kind,
                device_label=(device_label or None),
                signed_in_at=now,
            )
        )
        await session.commit()
    return IssuedRefresh(token=token, family_id=family_id)


async def create_refresh_token(
    user_id: uuid.UUID,
    family_id: uuid.UUID | None = None,
    absolute_expiry: datetime | None = None,
    *,
    kind: str = KIND_WEB,
    device_label: str | None = None,
) -> str:
    """
    Create and store a new refresh token for a user.

    Args:
        user_id: The user's ID
        family_id: Optional family ID for token rotation
        absolute_expiry: When the family ends (default: the kind's lifetime)
        kind: "web" or "app" (module docstring)
        device_label: the phone's name, for app sessions

    Returns:
        The plaintext refresh token
    """
    issued = await issue_refresh_token(
        user_id,
        kind=kind,
        device_label=device_label,
        family_id=family_id,
        absolute_expiry=absolute_expiry,
    )
    return issued.token


async def validate_refresh_token(token: str) -> RefreshToken | None:
    """Validate an active refresh token without rotating it."""
    token_hash = hash_token(token)

    async with async_session_maker() as session:
        result = await session.execute(
            select(RefreshToken).where(RefreshToken.token_hash == token_hash)
        )
        refresh_token = result.scalars().first()

        if not refresh_token:
            return None

        now = datetime.now(UTC)
        if (
            refresh_token.revoked_at is not None
            or now >= refresh_token.expires_at
            or (
                refresh_token.absolute_expiry is not None
                and now >= refresh_token.absolute_expiry
            )
        ):
            return None

        return refresh_token


async def rotate_refresh_token(token: str) -> RefreshRotationResult | None:
    """Atomically rotate a refresh token with access-only grace."""
    token_hash = hash_token(token)
    now = datetime.now(UTC)
    grace_window = timedelta(seconds=config.REFRESH_ROTATION_GRACE_SECONDS)

    async with async_session_maker() as session:
        async with session.begin():
            result = await session.execute(
                select(RefreshToken)
                .where(RefreshToken.token_hash == token_hash)
                .with_for_update()
            )
            refresh_token = result.scalars().first()

            if not refresh_token:
                return None
            user_id = refresh_token.user_id
            family_id = refresh_token.family_id

            if refresh_token.revoked_at is not None:
                if (
                    now - refresh_token.revoked_at <= grace_window
                    and now < refresh_token.expires_at
                ):
                    return RefreshRotationResult(
                        user_id=user_id,
                        refresh_token=None,
                        access_only=True,
                        family_id=family_id,
                    )

                await session.execute(
                    update(RefreshToken)
                    .where(RefreshToken.family_id == refresh_token.family_id)
                    .values(revoked_at=now, expires_at=now)
                )
                await _forget_push_devices(session, user_id, [family_id])
                logger.warning(f"Token reuse detected for user {user_id}")
                return None

            if now >= refresh_token.expires_at:
                return None

            kind = refresh_token.kind or KIND_WEB
            family_cap = refresh_token.absolute_expiry or (
                now + timedelta(seconds=family_lifetime_seconds(kind))
            )
            if now >= family_cap:
                return None

            new_plaintext = generate_refresh_token()
            # The child keeps the family's kind, device and sign-in time: a
            # phone stays the same phone, signed in when it was, across every
            # refresh (and after the daily purge removes the older rows).
            child = RefreshToken(
                user_id=user_id,
                token_hash=hash_token(new_plaintext),
                expires_at=token_expiry(kind, now, family_cap),
                family_id=refresh_token.family_id,
                absolute_expiry=family_cap,
                kind=kind,
                device_label=refresh_token.device_label,
                signed_in_at=refresh_token.signed_in_at or refresh_token.created_at,
            )
            session.add(child)
            refresh_token.revoked_at = now
            refresh_token.absolute_expiry = family_cap

        return RefreshRotationResult(
            user_id=user_id,
            refresh_token=new_plaintext,
            access_only=False,
            family_id=family_id,
        )


async def revoke_refresh_token(token: str) -> bool:
    """
    Revoke a single refresh token by its plaintext value.

    Args:
        token: The plaintext refresh token

    Returns:
        True if token was found and revoked, False otherwise
    """
    token_hash = hash_token(token)
    now = datetime.now(UTC)

    async with async_session_maker() as session:
        result = await session.execute(
            update(RefreshToken)
            .where(RefreshToken.token_hash == token_hash)
            .values(revoked_at=now, expires_at=now)
            .returning(RefreshToken.user_id, RefreshToken.family_id)
        )
        ended = result.all()
        # Signing out with the family's live token ends the family: its phone's pushes go too.
        for user_id, family_id in ended:
            await _forget_push_devices(session, user_id, [family_id])
        await session.commit()
        return len(ended) > 0


async def revoke_all_user_tokens(user_id: uuid.UUID) -> None:
    """Revoke all refresh tokens for a user (logout all devices)."""
    now = datetime.now(UTC)
    async with async_session_maker() as session:
        await session.execute(
            update(RefreshToken)
            .where(RefreshToken.user_id == user_id)
            .values(revoked_at=now, expires_at=now)
        )
        # Every sign-in ended (sign out everywhere, a password change, a deleted account): no phone takes pushes.
        await _forget_push_devices(session, user_id)
        await session.commit()


async def revoke_family(user_id: uuid.UUID, family_id: uuid.UUID) -> bool:
    """Sign one device out: every token of its family, this user's only."""
    now = datetime.now(UTC)
    async with async_session_maker() as session:
        result = await session.execute(
            update(RefreshToken)
            .where(
                RefreshToken.user_id == user_id,
                RefreshToken.family_id == family_id,
                RefreshToken.revoked_at.is_(None),
            )
            .values(revoked_at=now, expires_at=now)
        )
        # That device's push token goes with its sign-in (the lost phone stops getting pushes now).
        await _forget_push_devices(session, user_id, [family_id])
        await session.commit()
        return result.rowcount > 0


def devices_from_rows(
    rows: Iterable[RefreshToken], now: datetime
) -> list[DeviceSession]:
    """The signed-in devices among these rows (one account's), newest use first.

    A family is a device. It is signed in while it has one token that is
    neither revoked nor expired. The sign-in is dated by the ``signed_in_at``
    every token of the family carries (c0027), or, on rows older than that,
    by the family's earliest row; the latest row dates the last use (every
    refresh makes a row). The daily purge deletes rotated rows, which is why
    the sign-in time travels on each row.
    """
    first: dict[uuid.UUID, datetime] = {}
    last: dict[uuid.UUID, datetime] = {}
    live: dict[uuid.UUID, RefreshToken] = {}
    for row in rows:
        made = row.created_at
        began = getattr(row, "signed_in_at", None) or made
        if row.family_id not in first or began < first[row.family_id]:
            first[row.family_id] = began
        if row.family_id not in last or made > last[row.family_id]:
            last[row.family_id] = made
        cap = row.absolute_expiry
        usable = (
            row.revoked_at is None
            and now < row.expires_at
            and (cap is None or now < cap)
        )
        if usable:
            live[row.family_id] = row
    devices = [
        DeviceSession(
            family_id=family_id,
            kind=row.kind or KIND_WEB,
            device_label=row.device_label,
            signed_in_at=first[family_id],
            last_used_at=last[family_id],
            expires_at=min(row.expires_at, row.absolute_expiry)
            if row.absolute_expiry
            else row.expires_at,
        )
        for family_id, row in live.items()
    ]
    devices.sort(key=lambda d: d.last_used_at, reverse=True)
    return devices


async def list_devices(user_id: uuid.UUID) -> list[DeviceSession]:
    """Every device this account is signed in on (``GET /auth/devices``)."""
    now = datetime.now(UTC)
    async with async_session_maker() as session:
        result = await session.execute(
            select(RefreshToken).where(RefreshToken.user_id == user_id)
        )
        rows = result.scalars().all()
    return devices_from_rows(rows, now)
