"""The database behind push: devices, choices, tickets, and the unread count.

``Store`` is the protocol ``sending.py`` works against; tests use an in-memory
one (``tests/unit/push/fake_store.py``). ``DbStore`` is the real one.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Protocol
from uuid import UUID

from fastapi import Depends
from sqlalchemy import delete, exists, func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm.attributes import flag_modified

from app.db import RefreshToken, get_async_session
from app.notifications.persistence import Notification
from app.push.persistence import PushDevice, PushPreference, PushTicket
from app.site_announcements.expiry import unexpired


def live_family_clause(user_id, family_column, now: datetime):
    """A family of this user's with a token neither revoked nor expired (refresh_tokens.devices_from_rows' rule)."""
    return exists().where(
        RefreshToken.user_id == user_id,
        RefreshToken.family_id == family_column,
        RefreshToken.revoked_at.is_(None),
        RefreshToken.expires_at > now,
        or_(RefreshToken.absolute_expiry.is_(None), RefreshToken.absolute_expiry > now),
    )


class Store(Protocol):
    async def devices_of(self, user_id: UUID) -> list[PushDevice]: ...
    async def choices_of(self, user_id: UUID) -> dict | None: ...
    async def unread_count(self, user_id: UUID) -> int: ...
    async def add_ticket(self, ticket_id: str, token: str) -> None: ...
    async def remove_token(self, token: str) -> int: ...
    async def due_tickets(self, before: datetime) -> list[PushTicket]: ...
    async def delete_tickets(self, ticket_ids: list[str]) -> int: ...
    async def commit(self) -> None: ...


class DbStore:
    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    # -- devices --------------------------------------------------------------

    async def devices_of(
        self, user_id: UUID, now: datetime | None = None
    ) -> list[PushDevice]:
        """The phones that take this account's pushes: a phone bound to a sign-in only while it is live."""
        now = now or datetime.now(UTC)
        rows = await self._session.execute(
            select(PushDevice).where(
                PushDevice.user_id == user_id,
                or_(
                    PushDevice.session_family_id.is_(None),
                    live_family_clause(user_id, PushDevice.session_family_id, now),
                ),
            )
        )
        return list(rows.scalars().all())

    async def family_is_live(
        self, user_id: UUID, family_id: UUID, now: datetime | None = None
    ) -> bool:
        """Whether this sign-in is this account's and still live."""
        now = now or datetime.now(UTC)
        row = await self._session.execute(
            select(live_family_clause(user_id, family_id, now))
        )
        return bool(row.scalar())

    async def upsert_device(
        self,
        user_id: UUID,
        *,
        token: str,
        platform: str,
        label: str | None,
        language: str | None,
        session_family_id: UUID | None = None,
        now: datetime | None = None,
    ) -> PushDevice:
        """This phone, for this account: made, refreshed, or moved from another account."""
        now = now or datetime.now(UTC)
        device = (
            await self._session.execute(
                select(PushDevice).where(PushDevice.token == token)
            )
        ).scalar_one_or_none()
        if device is None:
            device = PushDevice(token=token, user_id=user_id, platform=platform)
            self._session.add(device)
        device.user_id = user_id
        device.platform = platform
        device.label = label
        device.language = language
        # Each registration names the sign-in it comes from (a phone signed in again has a new one).
        device.session_family_id = session_family_id
        device.last_seen_at = now
        await self._session.flush()
        return device

    async def remove_device(self, user_id: UUID, token: str) -> int:
        """Only the owning account may remove a token (sign-out)."""
        result = await self._session.execute(
            delete(PushDevice).where(
                PushDevice.user_id == user_id, PushDevice.token == token
            )
        )
        return result.rowcount or 0

    async def remove_token(self, token: str) -> int:
        """Expo said the phone no longer has the app: whoever's it was, it goes."""
        result = await self._session.execute(
            delete(PushDevice).where(PushDevice.token == token)
        )
        return result.rowcount or 0

    # -- choices --------------------------------------------------------------

    async def choices_of(self, user_id: UUID) -> dict | None:
        row = await self._session.get(PushPreference, user_id)
        return dict(row.choices or {}) if row is not None else None

    async def save_choices(self, user_id: UUID, choices: dict[str, bool]) -> dict:
        row = await self._session.get(PushPreference, user_id)
        if row is None:
            row = PushPreference(user_id=user_id, choices=dict(choices))
            self._session.add(row)
        else:
            row.choices = {**(row.choices or {}), **choices}
            flag_modified(row, "choices")
        await self._session.flush()
        return dict(row.choices)

    # -- the badge ------------------------------------------------------------

    async def unread_count(self, user_id: UUID) -> int:
        """What the Inbox tab's badge shows: unread, unexpired notifications."""
        count = await self._session.scalar(
            select(func.count(Notification.id)).where(
                Notification.user_id == user_id,
                Notification.read == False,  # noqa: E712
                unexpired(datetime.now(UTC)),
            )
        )
        return int(count or 0)

    # -- tickets --------------------------------------------------------------

    async def add_ticket(self, ticket_id: str, token: str) -> None:
        self._session.add(PushTicket(ticket_id=ticket_id, token=token))

    async def due_tickets(self, before: datetime) -> list[PushTicket]:
        rows = await self._session.execute(
            select(PushTicket).where(PushTicket.created_at < before)
        )
        return list(rows.scalars().all())

    async def delete_tickets(self, ticket_ids: list[str]) -> int:
        if not ticket_ids:
            return 0
        result = await self._session.execute(
            delete(PushTicket).where(PushTicket.ticket_id.in_(ticket_ids))
        )
        return result.rowcount or 0

    async def commit(self) -> None:
        await self._session.commit()


async def get_push_store(
    session: AsyncSession = Depends(get_async_session),
) -> DbStore:
    """The routes' store, over the request's session (tests give a fake)."""
    return DbStore(session)
