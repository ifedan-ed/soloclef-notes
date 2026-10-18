"""An in-memory push store: what ``app/push/store.py`` does, without a database."""

from __future__ import annotations

from datetime import UTC, datetime
from uuid import UUID

from app.push.persistence import PushDevice, PushTicket


class FakeStore:
    def __init__(
        self,
        *,
        unread: dict[UUID, int] | None = None,
        live_families: dict[UUID, set[UUID]] | None = None,
    ) -> None:
        self.devices: list[PushDevice] = []
        # Each user's live sign-ins (refresh-token families); DbStore reads refresh_tokens.
        self.live_families = live_families or {}
        self.choices: dict[UUID, dict] = {}
        self.tickets: list[PushTicket] = []
        self.unread = unread or {}
        self.commits = 0
        self._next_id = 1

    # -- devices --------------------------------------------------------------

    async def devices_of(self, user_id: UUID) -> list[PushDevice]:
        live = self.live_families.get(user_id, set())
        return [
            d
            for d in self.devices
            if d.user_id == user_id
            and (d.session_family_id is None or d.session_family_id in live)
        ]

    async def family_is_live(self, user_id: UUID, family_id: UUID) -> bool:
        return family_id in self.live_families.get(user_id, set())

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
        now = now or datetime.now(UTC)
        device = next((d for d in self.devices if d.token == token), None)
        if device is None:
            device = PushDevice(token=token, user_id=user_id, platform=platform)
            device.id = self._next_id
            self._next_id += 1
            self.devices.append(device)
        device.user_id = user_id
        device.platform = platform
        device.label = label
        device.language = language
        device.session_family_id = session_family_id
        device.last_seen_at = now
        return device

    async def remove_device(self, user_id: UUID, token: str) -> int:
        before = len(self.devices)
        self.devices = [
            d for d in self.devices if not (d.user_id == user_id and d.token == token)
        ]
        return before - len(self.devices)

    async def remove_token(self, token: str) -> int:
        before = len(self.devices)
        self.devices = [d for d in self.devices if d.token != token]
        return before - len(self.devices)

    # -- choices --------------------------------------------------------------

    async def choices_of(self, user_id: UUID) -> dict | None:
        saved = self.choices.get(user_id)
        return dict(saved) if saved is not None else None

    async def save_choices(self, user_id: UUID, choices: dict[str, bool]) -> dict:
        merged = {**self.choices.get(user_id, {}), **choices}
        self.choices[user_id] = merged
        return dict(merged)

    # -- the badge ------------------------------------------------------------

    async def unread_count(self, user_id: UUID) -> int:
        return self.unread.get(user_id, 0)

    # -- tickets --------------------------------------------------------------

    async def add_ticket(self, ticket_id: str, token: str) -> None:
        ticket = PushTicket(ticket_id=ticket_id, token=token)
        ticket.created_at = datetime.now(UTC)
        self.tickets.append(ticket)

    async def due_tickets(self, before: datetime) -> list[PushTicket]:
        return [t for t in self.tickets if t.created_at < before]

    async def delete_tickets(self, ticket_ids: list[str]) -> int:
        before = len(self.tickets)
        self.tickets = [t for t in self.tickets if t.ticket_id not in ticket_ids]
        return before - len(self.tickets)

    async def commit(self) -> None:
        self.commits += 1


def device(user_id: UUID, token: str, platform: str = "ios") -> PushDevice:
    d = PushDevice(token=token, user_id=user_id, platform=platform)
    d.last_seen_at = datetime.now(UTC)
    return d
