"""Who gets a push, what happens to dead tokens, and reading receipts."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest

from app.push.expo import Receipt, Ticket
from app.push.message import build_message
from app.push.sending import (
    RECEIPT_WAIT,
    TICKET_TTL,
    deliver,
    read_receipts,
    recipients,
)
from tests.unit.push.fake_store import FakeStore, device

pytestmark = pytest.mark.unit

ANN = uuid4()
BEN = uuid4()


class FakeClient:
    """Answers every token with a ticket; some tokens are "dead"."""

    def __init__(self, *, dead: set[str] = frozenset(), receipts=None) -> None:
        self.dead = set(dead)
        self.sent: list[list[dict]] = []
        self.asked: list[list[str]] = []
        self._receipts = receipts or []

    async def send(self, payloads: list[dict]) -> list[Ticket]:
        self.sent.append(payloads)
        return [
            Ticket(p["to"], None, "DeviceNotRegistered")
            if p["to"] in self.dead
            else Ticket(p["to"], f"ticket-{i}-{p['to']}", None)
            for i, p in enumerate(payloads)
        ]

    async def receipts(self, ticket_ids: list[str]) -> list[Receipt]:
        self.asked.append(ticket_ids)
        return [r for r in self._receipts if r.ticket_id in ticket_ids]


def test_recipients_every_phone_once_unless_the_kind_is_off() -> None:
    phones = [device(ANN, "ExponentPushToken[a]"), device(ANN, "ExponentPushToken[a]")]
    assert [d.token for d in recipients(phones, None, "answer")] == [
        "ExponentPushToken[a]"
    ]
    assert recipients(phones, {"answer": False}, "answer") == []
    assert recipients([], None, "answer") == []


async def test_deliver_sends_to_each_phone_with_the_badge_and_keeps_the_tickets() -> (
    None
):
    store = FakeStore(unread={ANN: 3})
    store.devices = [
        device(ANN, "ExponentPushToken[a1]"),
        device(ANN, "ExponentPushToken[a2]", "android"),
        device(BEN, "ExponentPushToken[b1]"),
    ]
    client = FakeClient()
    outcome = await deliver(
        store, client, user_id=ANN, message=build_message("mention")
    )
    assert outcome.reason == "sent"
    assert outcome.sent == 2
    assert [p["to"] for p in client.sent[0]] == [
        "ExponentPushToken[a1]",
        "ExponentPushToken[a2]",
    ]
    assert {p["badge"] for p in client.sent[0]} == {3}
    assert sorted(t.token for t in store.tickets) == [
        "ExponentPushToken[a1]",
        "ExponentPushToken[a2]",
    ]
    assert store.commits == 1


async def test_deliver_says_why_nothing_went() -> None:
    store = FakeStore()
    client = FakeClient()
    assert (
        await deliver(store, client, user_id=ANN, message=build_message("answer"))
    ).reason == "no_devices"
    store.devices = [device(ANN, "ExponentPushToken[a1]")]
    store.choices[ANN] = {"answer": False}
    assert (
        await deliver(store, client, user_id=ANN, message=build_message("answer"))
    ).reason == "kind_off"
    assert client.sent == []


async def test_a_phone_that_uninstalled_the_app_is_forgotten_at_once() -> None:
    store = FakeStore()
    store.devices = [
        device(ANN, "ExponentPushToken[gone]"),
        device(ANN, "ExponentPushToken[here]"),
    ]
    client = FakeClient(dead={"ExponentPushToken[gone]"})
    outcome = await deliver(
        store, client, user_id=ANN, message=build_message("announcement")
    )
    assert outcome.sent == 1
    assert outcome.dead == ["ExponentPushToken[gone]"]
    assert [d.token for d in store.devices] == ["ExponentPushToken[here]"]
    assert [t.token for t in store.tickets] == ["ExponentPushToken[here]"]


async def test_receipts_are_read_once_old_enough_and_dead_tokens_go() -> None:
    now = datetime(2026, 10, 10, 12, 0, tzinfo=UTC)
    store = FakeStore()
    store.devices = [
        device(ANN, "ExponentPushToken[dead]"),
        device(ANN, "ExponentPushToken[ok]"),
    ]
    await store.add_ticket("t-dead", "ExponentPushToken[dead]")
    await store.add_ticket("t-ok", "ExponentPushToken[ok]")
    await store.add_ticket("t-fresh", "ExponentPushToken[ok]")
    await store.add_ticket("t-stale", "ExponentPushToken[ok]")
    for ticket in store.tickets:
        ticket.created_at = now - RECEIPT_WAIT - timedelta(minutes=1)
    store.tickets[2].created_at = now  # sent just now: not due yet
    store.tickets[3].created_at = (
        now - TICKET_TTL - timedelta(hours=1)
    )  # too old to ask
    client = FakeClient(
        receipts=[
            Receipt("t-dead", "DeviceNotRegistered"),
            Receipt("t-ok", None),
        ]
    )

    read, dead = await read_receipts(store, client, now=now)

    assert read == 2
    assert dead == ["ExponentPushToken[dead]"]
    assert client.asked == [["t-dead", "t-ok"]]
    assert [t.ticket_id for t in store.tickets] == ["t-fresh"]
    assert [d.token for d in store.devices] == ["ExponentPushToken[ok]"]
    assert store.commits == 1


async def test_no_due_tickets_means_no_request() -> None:
    store = FakeStore()
    client = FakeClient()
    assert await read_receipts(store, client) == (0, [])
    assert client.asked == []
    assert store.commits == 0
