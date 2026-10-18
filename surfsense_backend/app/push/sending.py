"""Deciding who gets a push and sending it; reading receipts afterwards.

Pure decisions (``recipients``) sit apart from the shell (``deliver``,
``read_receipts``) that talks to the store and Expo, so the rules are
unit-tested with fakes and the shell stays thin.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from typing import Protocol
from uuid import UUID

from app.push.expo import Receipt, Ticket
from app.push.kinds import PushKind, wants
from app.push.message import PushMessage, expo_payload
from app.push.persistence import PushDevice
from app.push.store import Store

logger = logging.getLogger(__name__)

# Receipts are read once a ticket is this old (Expo has them within about
# 15 minutes), and a ticket nobody read in a day is dropped unread
# (custom/docs/push-notifications.md).
RECEIPT_WAIT = timedelta(minutes=15)
TICKET_TTL = timedelta(hours=24)


class PushClient(Protocol):
    async def send(self, payloads: list[dict]) -> list[Ticket]: ...
    async def receipts(self, ticket_ids: list[str]) -> list[Receipt]: ...


@dataclass(frozen=True, slots=True)
class Delivery:
    """What happened for one event: why nothing went, or what went where."""

    reason: str
    sent: int = 0
    dead: list[str] = field(default_factory=list)


def recipients(
    devices: list[PushDevice], choices: dict | None, kind: PushKind
) -> list[PushDevice]:
    """The devices a push of ``kind`` goes to: every phone, unless the kind is off."""
    if not devices or not wants(kind, choices):
        return []
    seen: set[str] = set()
    unique: list[PushDevice] = []
    for device in devices:
        if device.token in seen:
            continue
        seen.add(device.token)
        unique.append(device)
    return unique


async def deliver(
    store: Store, client: PushClient, *, user_id: UUID, message: PushMessage
) -> Delivery:
    """Send ``message`` to the person's phones; keep tickets, drop dead tokens."""
    devices = await store.devices_of(user_id)
    if not devices:
        return Delivery("no_devices")
    targets = recipients(devices, await store.choices_of(user_id), message.kind)
    if not targets:
        return Delivery("kind_off")
    badge = await store.unread_count(user_id)
    payloads = [expo_payload(message, d.token, badge=badge) for d in targets]
    tickets = await client.send(payloads)
    dead: list[str] = []
    for ticket in tickets:
        if ticket.ok and ticket.id:
            await store.add_ticket(ticket.id, ticket.token)
        elif ticket.dead_token:
            await store.remove_token(ticket.token)
            dead.append(ticket.token)
        else:
            logger.warning(
                "push: %s not accepted for one device: %s %s",
                message.kind,
                ticket.error,
                ticket.message or "",
            )
    await store.commit()
    return Delivery("sent", sent=sum(1 for t in tickets if t.ok), dead=dead)


async def read_receipts(
    store: Store, client: PushClient, *, now: datetime | None = None
) -> tuple[int, list[str]]:
    """Read the receipts of tickets old enough; returns (tickets read, dead tokens)."""
    now = now or datetime.now(UTC)
    due = await store.due_tickets(now - RECEIPT_WAIT)
    if not due:
        return 0, []
    fresh = [t for t in due if t.created_at and t.created_at > now - TICKET_TTL]
    stale_ids = [t.ticket_id for t in due if t not in fresh]
    token_of = {t.ticket_id: t.token for t in fresh}
    dead: list[str] = []
    receipts = await client.receipts(list(token_of)) if fresh else []
    read_ids: list[str] = []
    for receipt in receipts:
        read_ids.append(receipt.ticket_id)
        token = token_of.get(receipt.ticket_id)
        if receipt.dead_token and token:
            await store.remove_token(token)
            dead.append(token)
        elif receipt.error:
            logger.warning("push: receipt error %s %s", receipt.error, receipt.message)
    await store.delete_tickets(read_ids + stale_ids)
    await store.commit()
    return len(read_ids), dead
