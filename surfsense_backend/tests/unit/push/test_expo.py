"""Expo's push service over HTTP: batches of 100, tickets, receipts, failures."""

from __future__ import annotations

import json

import httpx
import pytest

from app.push.expo import (
    BATCH_SIZE,
    ExpoPushClient,
    Ticket,
    batches,
    parse_receipts,
    parse_tickets,
)

pytestmark = pytest.mark.unit


def _payloads(n: int) -> list[dict]:
    return [
        {"to": f"ExponentPushToken[{i}]", "title": "t", "body": "b"} for i in range(n)
    ]


def test_batches_of_a_hundred() -> None:
    assert BATCH_SIZE == 100
    assert [len(b) for b in batches(list(range(250)))] == [100, 100, 50]
    assert batches([]) == []


def test_tickets_follow_the_messages_order_and_odd_answers_become_errors() -> None:
    payloads = _payloads(3)
    body = {
        "data": [
            {"status": "ok", "id": "A"},
            {
                "status": "error",
                "message": "not registered",
                "details": {"error": "DeviceNotRegistered"},
            },
        ]
    }
    tickets = parse_tickets(payloads, body)
    assert tickets[0] == Ticket("ExponentPushToken[0]", "A", None)
    assert tickets[1].dead_token and tickets[1].message == "not registered"
    assert tickets[2].error == "UnexpectedResponse"  # the answer was short
    assert all(t.error == "UnexpectedResponse" for t in parse_tickets(payloads, "junk"))


def test_receipts() -> None:
    receipts = parse_receipts(
        {
            "data": {
                "A": {"status": "ok"},
                "B": {"status": "error", "details": {"error": "DeviceNotRegistered"}},
                "C": {"status": "error", "message": "odd"},
            }
        }
    )
    assert [(r.ticket_id, r.error) for r in receipts] == [
        ("A", None),
        ("B", "DeviceNotRegistered"),
        ("C", "Unknown"),
    ]
    assert parse_receipts({"data": []}) == []


async def test_the_client_sends_in_batches_with_the_access_token() -> None:
    seen: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        if request.url.path.endswith("/push/send"):
            sent = json.loads(request.content)
            return httpx.Response(
                200, json={"data": [{"status": "ok", "id": p["to"]} for p in sent]}
            )
        asked = json.loads(request.content)["ids"]
        return httpx.Response(200, json={"data": {i: {"status": "ok"} for i in asked}})

    client = ExpoPushClient(
        base_url="https://exp.test/",
        access_token="secret",
        transport=httpx.MockTransport(handler),
    )
    tickets = await client.send(_payloads(150))
    assert len(tickets) == 150 and all(t.ok for t in tickets)
    assert [str(r.url) for r in seen] == ["https://exp.test/--/api/v2/push/send"] * 2
    assert [len(json.loads(r.content)) for r in seen] == [100, 50]
    assert seen[0].headers["Authorization"] == "Bearer secret"
    assert seen[0].headers["Content-Type"] == "application/json"

    receipts = await client.receipts([t.id for t in tickets])
    assert len(receipts) == 150
    assert str(seen[-1].url) == "https://exp.test/--/api/v2/push/getReceipts"


async def test_a_failed_request_yields_send_failed_tickets_and_no_receipts() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(503, text="down")

    client = ExpoPushClient(
        base_url="https://exp.test", transport=httpx.MockTransport(handler)
    )
    tickets = await client.send(_payloads(2))
    assert [t.error for t in tickets] == ["SendFailed", "SendFailed"]
    assert await client.receipts(["A"]) == []


async def test_no_access_token_means_no_authorization_header() -> None:
    seen: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return httpx.Response(200, json={"data": [{"status": "ok", "id": "x"}]})

    client = ExpoPushClient(transport=httpx.MockTransport(handler))
    await client.send(_payloads(1))
    assert "Authorization" not in seen[0].headers
    assert str(seen[0].url).startswith("https://exp.host/")
