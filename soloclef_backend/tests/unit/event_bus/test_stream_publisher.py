"""``add``: one XADD, with the key, the field map and the retention cap."""

from __future__ import annotations

from typing import Any

import pytest

from app.event_bus.event import Event
from app.event_bus.stream.publisher import add
from app.event_bus.stream.wire import FIELD, RETENTION_ENTRIES, STREAM

pytestmark = pytest.mark.unit


class _RecordingClient:
    def __init__(self) -> None:
        self.calls: list[dict[str, Any]] = []

    async def xadd(self, name: str, fields: dict[str, str], **options: Any) -> str:
        self.calls.append({"name": name, "fields": fields, **options})
        return "1762579661077-0"


async def test_add_writes_the_event_to_the_stream() -> None:
    client = _RecordingClient()
    event = Event(event_type="x.happened", payload={"k": "v"}, workspace_id=1)

    entry_id = await add(client, event)

    assert entry_id == "1762579661077-0"
    assert len(client.calls) == 1
    call = client.calls[0]
    assert call["name"] == STREAM
    assert list(call["fields"]) == [FIELD]


async def test_add_caps_the_history_it_keeps() -> None:
    """Approximate trimming, so a publish never pays for an exact length."""
    client = _RecordingClient()

    await add(client, Event(event_type="x.happened", workspace_id=1))

    call = client.calls[0]
    assert call["maxlen"] == RETENTION_ENTRIES
    assert call["approximate"] is True
