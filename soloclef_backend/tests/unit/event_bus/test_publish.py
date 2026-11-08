"""``publish``: stamp the event and hand it to the stream, and nothing else.

A producer's only job is to state the fact. Whether a subscriber runs, and in
which process, is the dispatcher's business — so these tests assert on what
reached the stream, never on a subscriber.
"""

from __future__ import annotations

import pytest

from app.event_bus import publish as publish_module
from app.event_bus.event import Event
from app.event_bus.stream.wire import decode

pytestmark = pytest.mark.unit


@pytest.fixture
def published(monkeypatch) -> list[Event]:
    """Capture what ``publish`` put on the stream instead of reaching Redis."""
    captured: list[Event] = []

    async def record(event: Event) -> str:
        captured.append(event)
        return "0-1"

    monkeypatch.setattr(publish_module, "add_with_own_client", record)
    return captured


async def test_publish_stamps_the_event_and_puts_it_on_the_stream(
    published: list[Event],
) -> None:
    returned = await publish_module.publish(
        "document.indexed", {"document_id": 42}, workspace_id=7
    )

    assert len(published) == 1
    assert published[0] == returned
    assert returned.event_type == "document.indexed"
    assert returned.payload == {"document_id": 42}
    assert returned.workspace_id == 7
    assert returned.event_id
    assert returned.occurred_at


async def test_publish_defaults_payload_to_empty_dict(published: list[Event]) -> None:
    await publish_module.publish("x.happened", workspace_id=1)

    assert published[0].payload == {}


async def test_what_publish_sends_is_what_a_dispatcher_reads_back(
    published: list[Event],
) -> None:
    """The round trip is the whole promise; assert it rather than the fields."""
    from app.event_bus.stream.wire import encode

    returned = await publish_module.publish("x.happened", {"k": "v"}, workspace_id=2)

    assert decode(encode(published[0])) == returned


async def test_a_stream_that_will_not_take_the_event_fails_the_publish(
    monkeypatch,
) -> None:
    """The caller logs it and the event is lost, as a failed fan-out was."""

    async def refuse(_event: Event) -> str:
        raise RuntimeError("redis is down")

    monkeypatch.setattr(publish_module, "add_with_own_client", refuse)

    with pytest.raises(RuntimeError):
        await publish_module.publish("x.happened", workspace_id=1)
