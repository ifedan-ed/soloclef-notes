"""The stream's names and its entry shape — the contract every publisher meets.

A publisher outside this interpreter (``soloclef_api/internal/eventstream``)
writes the same key, the same field and the same JSON, so a subscriber cannot
tell which language stated the fact.
"""

from __future__ import annotations

from typing import Any

from app.event_bus.event import Event

STREAM = "soloclef:events"
GROUP = "soloclef:events:dispatch"
FIELD = "event"

# A ceiling on the history an operator can read back, not a delivery window:
# what a dispatcher still owes is the group's cursor, not this cap.
RETENTION_ENTRIES = 10_000


def encode(event: Event) -> dict[str, str]:
    """The field map for one entry."""
    return {FIELD: event.model_dump_json()}


def decode(fields: dict[str, Any]) -> Event:
    """The event an entry carries. Raises when the entry is not one."""
    return Event.model_validate_json(fields[FIELD])
