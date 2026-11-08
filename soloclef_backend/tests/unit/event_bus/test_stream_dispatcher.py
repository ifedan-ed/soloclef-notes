"""``StreamDispatcher``: read the group, acknowledge, hand each event to the bus.

What the subscribers see is the contract: the same events, with the same
payloads, in the order the stream holds them, and a subscriber that raises
isolated exactly as the in-process fan-out isolated it.
"""

from __future__ import annotations

import asyncio
from typing import Any

import pytest
from redis.exceptions import ConnectionError, ResponseError

from app.event_bus.bus import EventBus
from app.event_bus.event import Event
from app.event_bus.stream import dispatcher as dispatcher_module
from app.event_bus.stream.dispatcher import StreamDispatcher
from app.event_bus.stream.wire import GROUP, STREAM, encode

pytestmark = pytest.mark.unit


class _FakeStream:
    """A stream that hands out the batches it was given, then nothing."""

    def __init__(self, batches: list[list[tuple[str, dict[str, str]]]]) -> None:
        self._batches = list(batches)
        self.acked: list[str] = []
        self.groups: list[tuple[str, str]] = []
        self.group_failure: Exception | None = None

    async def xgroup_create(self, name: str, group: str, **_: Any) -> None:
        if self.group_failure is not None:
            raise self.group_failure
        self.groups.append((name, group))

    async def xreadgroup(
        self, group: str, consumer: str, streams: dict[str, str], **_: Any
    ) -> list[tuple[str, list[tuple[str, dict[str, str]]]]]:
        if not self._batches:
            return []
        return [(STREAM, self._batches.pop(0))]

    async def xack(self, name: str, group: str, entry_id: str) -> int:
        self.acked.append(entry_id)
        return 1


def _entry(entry_id: str, event: Event) -> tuple[str, dict[str, str]]:
    return (entry_id, encode(event))


def _event(event_type: str = "x.happened", **payload: Any) -> Event:
    return Event(event_type=event_type, payload=payload, workspace_id=1)


def _dispatcher(stream: _FakeStream, bus: EventBus) -> StreamDispatcher:
    return StreamDispatcher(stream, bus=bus, consumer="test", block_ms=None)


async def test_each_event_reaches_every_subscriber() -> None:
    first, second = _event(n=1), _event(n=2)
    stream = _FakeStream([[_entry("1-0", first), _entry("2-0", second)]])
    bus = EventBus()
    seen: list[Event] = []
    bus.subscribe(lambda event: _collect(seen, event))

    delivered = await _dispatcher(stream, bus).drain_once()

    assert delivered == 2
    assert seen == [first, second]


async def test_events_are_delivered_in_the_order_the_stream_holds_them() -> None:
    """Two producers interleave in the stream; a subscriber sees that order."""
    events = [_event(n=n) for n in range(5)]
    stream = _FakeStream([[_entry(f"{n}-0", event) for n, event in enumerate(events)]])
    bus = EventBus()
    seen: list[Event] = []
    bus.subscribe(lambda event: _collect(seen, event))

    await _dispatcher(stream, bus).drain_once()

    assert [e.payload["n"] for e in seen] == [0, 1, 2, 3, 4]


async def test_every_entry_read_is_acknowledged() -> None:
    stream = _FakeStream([[_entry("1-0", _event()), _entry("2-0", _event())]])

    await _dispatcher(stream, EventBus()).drain_once()

    assert stream.acked == ["1-0", "2-0"]


async def test_a_subscriber_that_raises_does_not_stop_the_others() -> None:
    stream = _FakeStream([[_entry("1-0", _event())]])
    bus = EventBus()
    healthy: list[Event] = []

    async def boom(_event_: Event) -> None:
        raise RuntimeError("subscriber blew up")

    bus.subscribe(boom)
    bus.subscribe(lambda event: _collect(healthy, event))

    assert await _dispatcher(stream, bus).drain_once() == 1
    assert len(healthy) == 1


async def test_a_subscriber_that_raises_does_not_hold_the_entry() -> None:
    """The entry is acknowledged either way, so nothing is redelivered."""
    stream = _FakeStream([[_entry("1-0", _event())]])
    bus = EventBus()

    async def boom(_event_: Event) -> None:
        raise RuntimeError("subscriber blew up")

    bus.subscribe(boom)
    await _dispatcher(stream, bus).drain_once()

    assert stream.acked == ["1-0"]


async def test_an_entry_that_is_not_an_event_is_dropped_not_retried() -> None:
    stream = _FakeStream([[("1-0", {"event": '{"nonsense": true}'})]])
    bus = EventBus()
    seen: list[Event] = []
    bus.subscribe(lambda event: _collect(seen, event))

    assert await _dispatcher(stream, bus).drain_once() == 1
    assert seen == []
    assert stream.acked == ["1-0"]


async def test_an_empty_read_delivers_nothing() -> None:
    assert await _dispatcher(_FakeStream([]), EventBus()).drain_once() == 0


async def test_the_group_is_created_once() -> None:
    stream = _FakeStream([])

    await _dispatcher(stream, EventBus()).ensure_group()

    assert stream.groups == [(STREAM, GROUP)]


async def test_a_group_that_already_exists_is_not_an_error() -> None:
    """Every replica calls this at startup; only the first one creates it."""
    stream = _FakeStream([])
    stream.group_failure = ResponseError("BUSYGROUP Consumer Group name already exists")

    await _dispatcher(stream, EventBus()).ensure_group()


async def test_any_other_group_failure_is_raised() -> None:
    stream = _FakeStream([])
    stream.group_failure = ResponseError("NOPERM this user has no permissions")

    with pytest.raises(ResponseError):
        await _dispatcher(stream, EventBus()).ensure_group()


async def _collect(into: list[Event], event: Event) -> None:
    into.append(event)


class _UnreachableUntil:
    """A stream that refuses everything until the nth attempt, then stops."""

    def __init__(self, failures: int, stop: asyncio.Event) -> None:
        self.remaining = failures
        self.reads = 0
        self._stop = stop

    async def xgroup_create(self, *_: Any, **__: Any) -> None:
        self._maybe_fail()

    async def xreadgroup(self, *_: Any, **__: Any) -> list[Any]:
        self._maybe_fail()
        self.reads += 1
        self._stop.set()
        return []

    def _maybe_fail(self) -> None:
        if self.remaining > 0:
            self.remaining -= 1
            raise ConnectionError("redis is down")


async def test_a_redis_that_is_down_is_waited_out_not_fatal(monkeypatch) -> None:
    """The process this runs in must keep serving while the stream is down."""
    monkeypatch.setattr(dispatcher_module, "RETRY_SECONDS", 0.001)
    stop = asyncio.Event()
    stream = _UnreachableUntil(failures=3, stop=stop)
    dispatcher = StreamDispatcher(
        stream, bus=EventBus(), consumer="test", block_ms=None
    )

    await asyncio.wait_for(dispatcher.run(stop), 5)

    assert stream.reads == 1
