"""Expo's push service, over HTTP.

``POST {EXPO_PUSH_URL}/--/api/v2/push/send`` takes up to ``BATCH_SIZE``
messages and answers one ticket per message; ``.../push/getReceipts`` answers
one receipt per ticket id, available about ``RECEIPT_WAIT`` after sending
(``sending.py``). A ``DeviceNotRegistered`` error, in a ticket or a receipt,
means the phone no longer has the app: its token is removed.

Only the transport lives here; what to do with tickets is ``sending.py``.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any

import httpx

logger = logging.getLogger(__name__)

# Expo takes at most 100 messages per request (custom/docs/push-notifications.md).
BATCH_SIZE = 100
# One request to Expo may take this long before it is given up.
TIMEOUT_SECONDS = 10.0
DEVICE_NOT_REGISTERED = "DeviceNotRegistered"


@dataclass(frozen=True, slots=True)
class Ticket:
    """Expo's answer for one message: an id to read the receipt with, or an error."""

    token: str
    id: str | None
    error: str | None
    message: str | None = None

    @property
    def ok(self) -> bool:
        return self.id is not None and self.error is None

    @property
    def dead_token(self) -> bool:
        return self.error == DEVICE_NOT_REGISTERED


@dataclass(frozen=True, slots=True)
class Receipt:
    ticket_id: str
    error: str | None
    message: str | None = None

    @property
    def dead_token(self) -> bool:
        return self.error == DEVICE_NOT_REGISTERED


def batches(items: list[Any], size: int = BATCH_SIZE) -> list[list[Any]]:
    return [items[i : i + size] for i in range(0, len(items), size)]


def parse_tickets(payloads: list[dict], body: Any) -> list[Ticket]:
    """Expo answers in the messages' order; a short or odd answer yields errors."""
    data = body.get("data") if isinstance(body, dict) else None
    if not isinstance(data, list):
        return [
            Ticket(p["to"], None, "UnexpectedResponse", "no data list")
            for p in payloads
        ]
    tickets: list[Ticket] = []
    for payload, entry in zip(payloads, data, strict=False):
        token = payload["to"]
        if not isinstance(entry, dict):
            tickets.append(Ticket(token, None, "UnexpectedResponse", str(entry)))
        elif entry.get("status") == "ok" and isinstance(entry.get("id"), str):
            tickets.append(Ticket(token, entry["id"], None))
        else:
            details = entry.get("details") or {}
            error = details.get("error") if isinstance(details, dict) else None
            tickets.append(
                Ticket(token, None, error or "Unknown", entry.get("message"))
            )
    for payload in payloads[len(data) :]:
        tickets.append(Ticket(payload["to"], None, "UnexpectedResponse", "short"))
    return tickets


def parse_receipts(body: Any) -> list[Receipt]:
    data = body.get("data") if isinstance(body, dict) else None
    if not isinstance(data, dict):
        return []
    receipts: list[Receipt] = []
    for ticket_id, entry in data.items():
        if not isinstance(entry, dict) or entry.get("status") == "ok":
            receipts.append(Receipt(ticket_id, None))
            continue
        details = entry.get("details") or {}
        error = details.get("error") if isinstance(details, dict) else None
        receipts.append(Receipt(ticket_id, error or "Unknown", entry.get("message")))
    return receipts


class ExpoPushClient:
    def __init__(
        self,
        *,
        base_url: str = "https://exp.host",
        access_token: str | None = None,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._access_token = access_token or None
        self._transport = transport

    def _headers(self) -> dict[str, str]:
        headers = {
            "Accept": "application/json",
            "Accept-Encoding": "gzip, deflate",
            "Content-Type": "application/json",
        }
        if self._access_token:
            headers["Authorization"] = f"Bearer {self._access_token}"
        return headers

    def _client(self) -> httpx.AsyncClient:
        return httpx.AsyncClient(
            base_url=self._base_url,
            headers=self._headers(),
            timeout=TIMEOUT_SECONDS,
            transport=self._transport,
        )

    async def send(self, payloads: list[dict]) -> list[Ticket]:
        """Send every payload, ``BATCH_SIZE`` at a time; one ticket per payload."""
        tickets: list[Ticket] = []
        async with self._client() as client:
            for batch in batches(payloads):
                try:
                    response = await client.post("/--/api/v2/push/send", json=batch)
                    response.raise_for_status()
                    body = response.json()
                except (httpx.HTTPError, ValueError) as exc:
                    logger.warning("push: Expo send failed: %s", type(exc).__name__)
                    tickets.extend(
                        Ticket(p["to"], None, "SendFailed", type(exc).__name__)
                        for p in batch
                    )
                    continue
                tickets.extend(parse_tickets(batch, body))
        return tickets

    async def receipts(self, ticket_ids: list[str]) -> list[Receipt]:
        """The receipts for these tickets; a failed request yields none."""
        receipts: list[Receipt] = []
        async with self._client() as client:
            for batch in batches(ticket_ids):
                try:
                    response = await client.post(
                        "/--/api/v2/push/getReceipts", json={"ids": batch}
                    )
                    response.raise_for_status()
                    body = response.json()
                except (httpx.HTTPError, ValueError) as exc:
                    logger.warning("push: Expo receipts failed: %s", type(exc).__name__)
                    continue
                receipts.extend(parse_receipts(body))
        return receipts
