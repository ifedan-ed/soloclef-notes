"""A running turn's steps reach its saved row, so other open pages see them."""

from __future__ import annotations

import asyncio

import pytest

from app.tasks.chat.content_builder import AssistantContentBuilder
from app.tasks.chat.streaming.activity_timing import ActivityTimer
from app.tasks.chat.streaming.flows.shared.live_progress import LiveProgressSaver
from app.tasks.chat.streaming.relay.state import AgentEventRelayState
from app.tasks.chat.streaming.shared.stream_result import StreamResult

pytestmark = pytest.mark.unit


class _Clock:
    def __init__(self) -> None:
        self.now = 100.0

    def __call__(self) -> float:
        return self.now


class _Sleep:
    """Waits no real time: moves the fake clock on and lets others run."""

    def __init__(self, clock: _Clock) -> None:
        self.clock = clock
        self.delays: list[float] = []

    async def __call__(self, delay: float) -> None:
        self.delays.append(delay)
        self.clock.now += delay
        await asyncio.sleep(0)


async def _let_background_run() -> None:
    for _ in range(5):
        await asyncio.sleep(0)


def _running_activity(activity_id: str, title: str) -> dict:
    return {
        "id": activity_id,
        "sequence": 1,
        "kind": "web.crawl",
        "status": "running",
        "title": title,
        "category": "research",
        "iconKey": "scan-text",
        "startedAt": "2026-10-09T17:29:37+00:00",
    }


def _turn() -> StreamResult:
    result = StreamResult()
    result.assistant_message_id = 123
    result.content_builder = AssistantContentBuilder()
    result.activity_timer = ActivityTimer.start(now_ns=0)
    return result


async def test_saves_the_running_step_with_the_time_so_far():
    writes: list[tuple[int, list[dict]]] = []

    async def write(message_id, parts):
        writes.append((message_id, parts))

    turn = _turn()
    turn.content_builder.on_activity(_running_activity("a1", "Reviewing the web"))
    saver = LiveProgressSaver(clock=_Clock(), write=write)

    assert await saver.maybe_save(turn) is True
    message_id, parts = writes[0]
    assert message_id == 123
    journal = parts[0]
    assert journal["type"] == "data-activities"
    assert journal["data"]["activities"][0]["title"] == "Reviewing the web"
    assert journal["data"]["activities"][0]["status"] == "running"
    assert journal["data"]["timing"]["status"] == "running"


async def test_saves_at_most_once_per_interval_and_only_when_something_changed():
    writes: list[list[dict]] = []

    async def write(_message_id, parts):
        writes.append(parts)

    clock = _Clock()
    turn = _turn()
    turn.content_builder.on_activity(_running_activity("a1", "Searching email"))
    saver = LiveProgressSaver(
        interval_seconds=3.0, clock=clock, write=write, sleep=_Sleep(clock)
    )

    assert await saver.maybe_save(turn) is True
    turn.content_builder.on_text_delta("t1", "Partial answer")
    clock.now += 1.0
    assert await saver.maybe_save(turn) is False  # too soon
    clock.now += 3.0
    assert await saver.maybe_save(turn) is True  # text changed
    clock.now += 10.0
    assert await saver.maybe_save(turn) is False  # only the clock moved
    assert len(writes) == 2
    assert writes[1][-1] == {"type": "text", "text": "Partial answer"}


async def test_nothing_is_saved_before_the_row_exists_or_after_the_turn_ends():
    async def write(_message_id, _parts):
        raise AssertionError("must not write")

    turn = StreamResult()
    turn.content_builder = AssistantContentBuilder()
    saver = LiveProgressSaver(clock=_Clock(), write=write)
    assert await saver.maybe_save(turn) is False  # no assistant row yet

    finished = _turn()
    finished.activity_timer.complete(now_ns=1_000_000)
    assert await saver.maybe_save(finished) is False  # finalize writes it


async def test_a_failed_save_does_not_break_the_turn():
    async def write(_message_id, _parts):
        raise RuntimeError("database away")

    turn = _turn()
    turn.content_builder.on_activity(_running_activity("a1", "Reading pages"))
    saver = LiveProgressSaver(clock=_Clock(), write=write)
    assert await saver.maybe_save(turn) is False


async def test_the_parts_are_looked_at_once_per_interval_not_per_frame():
    async def write(_message_id, _parts):
        return None

    class _CountingBuilder(AssistantContentBuilder):
        copies = 0

        def snapshot(self):
            _CountingBuilder.copies += 1
            return super().snapshot()

    clock = _Clock()
    turn = _turn()
    turn.content_builder = _CountingBuilder()
    turn.content_builder.on_activity(_running_activity("a1", "Reading pages"))
    saver = LiveProgressSaver(
        interval_seconds=3.0, clock=clock, write=write, sleep=_Sleep(clock)
    )

    assert await saver.maybe_save(turn) is True
    clock.now += 3.0
    for _ in range(50):  # many frames in one interval, nothing changed
        await saver.maybe_save(turn)
    await saver.aclose()
    assert _CountingBuilder.copies == 2


async def test_a_change_inside_the_window_is_saved_when_the_window_ends():
    """No frame may follow it for a minute (a page fetch, a specialist's model call)."""
    writes: list[list[dict]] = []

    async def write(_message_id, parts):
        writes.append(parts)

    clock = _Clock()
    sleep = _Sleep(clock)
    turn = _turn()
    turn.content_builder.on_activity(_running_activity("a1", "Searching email"))
    saver = LiveProgressSaver(
        interval_seconds=3.0, clock=clock, write=write, sleep=sleep
    )
    assert await saver.maybe_save(turn) is True

    clock.now += 1.0
    turn.content_builder.on_activity(_running_activity("a2", "Reading pages (1/3)"))
    assert await saver.maybe_save(turn) is False  # inside the window
    assert len(writes) == 1
    await _let_background_run()  # ...and then no frame comes

    assert sleep.delays == [2.0]  # booked for the end of the window
    assert len(writes) == 2
    titles = [a["title"] for a in writes[1][0]["data"]["activities"]]
    assert "Reading pages (1/3)" in titles
    await saver.aclose()


async def test_a_tool_call_starting_or_ending_is_saved_without_waiting():
    writes: list[list[dict]] = []

    async def write(_message_id, parts):
        writes.append(parts)

    clock = _Clock()
    sleep = _Sleep(clock)
    turn = _turn()
    turn.activity_state = AgentEventRelayState()
    turn.content_builder.on_activity(_running_activity("a1", "Asking the web crawler"))
    saver = LiveProgressSaver(
        interval_seconds=3.0, clock=clock, write=write, sleep=sleep
    )
    assert await saver.maybe_save(turn) is True

    clock.now += 0.5
    # A specialist's tool call starts: its step is in the parts by the time
    # the turn next waits for the agent.
    turn.activity_state.tool_runs_started_or_ended += 1
    turn.content_builder.on_activity(_running_activity("a2", "Reading pages (1/3)"))
    assert await saver.maybe_save(turn) is False
    await _let_background_run()

    assert sleep.delays == [0.0]
    assert len(writes) == 2
    await saver.aclose()


async def test_a_stop_mid_save_cancels_the_turn_not_the_write():
    writes: list[list[dict]] = []
    database = asyncio.Event()

    async def slow_write(_message_id, parts):
        await database.wait()
        writes.append(parts)

    turn = _turn()
    turn.content_builder.on_activity(_running_activity("a1", "Reading pages"))
    saver = LiveProgressSaver(clock=_Clock(), write=slow_write)

    turn_task = asyncio.create_task(saver.maybe_save(turn))
    await _let_background_run()
    turn_task.cancel()  # Stop lands while the row is being written
    with pytest.raises(asyncio.CancelledError):
        await turn_task

    closing = asyncio.create_task(saver.aclose())  # the turn's cleanup
    await _let_background_run()
    assert not closing.done(), "finalize waits for the write still going"
    database.set()
    await closing
    assert len(writes) == 1
    # After close nothing more is written: finalize's answer is the last word.
    turn.content_builder.on_text_delta("t1", "late text")
    assert await saver.maybe_save(turn) is False
    assert len(writes) == 1


async def test_a_look_booked_for_later_is_dropped_when_the_turn_ends():
    saved: list[str] = []

    async def write(_message_id, _parts):
        saved.append("write")

    clock = _Clock()
    turn = _turn()
    turn.content_builder.on_activity(_running_activity("a1", "Reading pages"))
    saver = LiveProgressSaver(
        interval_seconds=3.0, clock=clock, write=write, sleep=_Sleep(clock)
    )
    assert await saver.maybe_save(turn) is True
    clock.now += 1.0
    turn.content_builder.on_text_delta("t1", "more")
    assert await saver.maybe_save(turn) is False  # a look is booked
    await saver.aclose()  # the turn ends before it is due
    await _let_background_run()
    assert saved == ["write"]
