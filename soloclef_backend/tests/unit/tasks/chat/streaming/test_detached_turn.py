"""A chat turn outlives the page that started it, and Stop still ends it."""

from __future__ import annotations

import asyncio

import anyio
import pytest
from fastapi import HTTPException

from app.tasks.chat.streaming.detached_turn import (
    KEEPALIVE_FRAME,
    is_turn_running,
    is_turn_stopping,
    registry,
    run_cleanup_to_the_end,
    start_turn_in_background,
    stop_running_turn,
)
from app.tasks.chat.streaming.errors.messages import chat_error_message

pytestmark = pytest.mark.unit


async def _wait_until_finished(thread_key: str, timeout: float = 2.0) -> None:
    with anyio.fail_after(timeout):
        while is_turn_running(thread_key):
            await asyncio.sleep(0.01)


async def test_the_turn_finishes_and_saves_after_the_page_goes_away():
    produced: list[str] = []
    saved = asyncio.Event()
    page_left = asyncio.Event()

    async def turn():
        try:
            produced.append("first")
            yield "first"
            await page_left.wait()
            produced.append("second")
            yield "second"
        finally:
            saved.set()

    page = start_turn_in_background("detached-1", turn())
    assert await anext(page) == "first"
    await page.aclose()  # the tab was closed / reloaded
    assert is_turn_running("detached-1")

    page_left.set()
    await _wait_until_finished("detached-1")
    assert produced == ["first", "second"]
    assert saved.is_set()


async def test_stop_cancels_the_turn_and_its_shielded_cleanup_still_saves():
    saved: list[str] = []
    started = asyncio.Event()

    async def turn():
        try:
            yield "working"
            started.set()
            await asyncio.Event().wait()  # a model call that never returns
        finally:
            with anyio.CancelScope(shield=True):
                await asyncio.sleep(0.01)
                saved.append("partial answer")

    page = start_turn_in_background("detached-2", turn())
    assert await anext(page) == "working"
    await started.wait()

    assert await stop_running_turn("detached-2", wait_seconds=2) is True
    assert saved == ["partial answer"]
    assert not is_turn_running("detached-2")
    # The page that was listening sees the stream end rather than hang.
    with anyio.fail_after(1):
        assert [frame async for frame in page] == []


async def test_stop_says_so_when_nothing_is_running():
    assert await stop_running_turn("detached-none", wait_seconds=0.1) is False


async def test_a_silent_turn_sends_keepalive_comments_to_the_page():
    async def turn():
        await asyncio.sleep(0.08)
        yield "data: answer\n\n"

    page = start_turn_in_background("detached-3", turn(), keepalive_seconds=0.02)
    with anyio.fail_after(2):
        frames = [frame async for frame in page]
    assert frames[-1] == "data: answer\n\n"
    assert frames[:-1] and set(frames[:-1]) == {KEEPALIVE_FRAME}
    assert KEEPALIVE_FRAME.startswith(":")  # an SSE comment: clients skip it


async def test_turn_status_is_busy_from_the_start_until_the_answer_is_saved():
    from app.routes.new_chat_routes import (
        _build_turn_status_payload,
        _raise_if_thread_busy_for_start,
    )

    release = asyncio.Event()

    async def turn():
        await release.wait()
        yield "done"

    page = start_turn_in_background(424242, turn())
    # The agent has not taken the thread lock yet, but a turn is running, so
    # a second send or Regenerate is refused rather than racing it.
    assert _build_turn_status_payload(424242) == {"status": "busy"}
    with pytest.raises(HTTPException) as refused:
        _raise_if_thread_busy_for_start(424242)
    assert refused.value.status_code == 409
    assert refused.value.detail["errorCode"] == "THREAD_BUSY"
    release.set()
    assert [frame async for frame in page] == ["done"]
    await _wait_until_finished("424242")
    assert _build_turn_status_payload(424242) == {"status": "idle"}


def test_the_busy_message_says_what_to_do():
    message = chat_error_message("THREAD_BUSY")
    assert "another tab" in message
    assert "Stop" in message


def _flow_shaped_turn(log: list[str], *, body: asyncio.Event, cleanup_started):
    """A turn whose ``finally`` saves the way the chat flows do."""

    async def turn():
        try:
            yield "working"
            await body.wait()
            yield "done"
        finally:
            log.append("lock released")
            cleanup_started.set()

            async def save_and_release():
                await asyncio.sleep(0.05)  # credit hold, "AI is responding"
                log.append("flag cleared")
                await asyncio.sleep(0.05)  # the reply and its token usage
                log.append("answer saved")

            await run_cleanup_to_the_end(save_and_release())

    return turn()


async def test_a_second_stop_does_not_cut_the_save_short():
    log: list[str] = []
    cleanup_started = asyncio.Event()
    never = asyncio.Event()
    page = start_turn_in_background(
        "detached-twice",
        _flow_shaped_turn(log, body=never, cleanup_started=cleanup_started),
    )
    assert await anext(page) == "working"

    first = asyncio.create_task(stop_running_turn("detached-twice", wait_seconds=2))
    await cleanup_started.wait()
    assert is_turn_stopping("detached-twice")
    second = asyncio.create_task(stop_running_turn("detached-twice", wait_seconds=2))
    assert await first is True
    assert await second is True

    assert log == ["lock released", "flag cleared", "answer saved"]
    assert not is_turn_running("detached-twice")
    assert not is_turn_stopping("detached-twice")


async def test_stop_while_the_turn_is_finishing_waits_for_the_save():
    log: list[str] = []
    cleanup_started = asyncio.Event()
    body = asyncio.Event()
    body.set()  # the turn runs to its end on its own
    page = start_turn_in_background(
        "detached-finishing",
        _flow_shaped_turn(log, body=body, cleanup_started=cleanup_started),
    )
    tasks = list(registry._running["detached-finishing"])
    assert await anext(page) == "working"
    assert await anext(page) == "done"
    await cleanup_started.wait()

    assert await stop_running_turn("detached-finishing", wait_seconds=2) is True
    assert log == ["lock released", "flag cleared", "answer saved"]
    # The turn had already finished: it ends as finished, not as stopped.
    assert not tasks[0].cancelled()
    assert tasks[0].cancelling() == 0


async def test_cleanup_finishes_whatever_cancels_reach_the_turn():
    """Even cancels that bypass Stop's once-only rule cannot cut it short."""
    log: list[str] = []
    cleanup_started = asyncio.Event()
    never = asyncio.Event()
    page = start_turn_in_background(
        "detached-raw-cancels",
        _flow_shaped_turn(log, body=never, cleanup_started=cleanup_started),
    )
    assert await anext(page) == "working"
    (task,) = registry._running["detached-raw-cancels"]

    task.cancel()
    await cleanup_started.wait()
    for _ in range(3):
        task.cancel()
        await asyncio.sleep(0.02)
    with anyio.fail_after(2):
        await asyncio.wait([task])

    assert log == ["lock released", "flag cleared", "answer saved"]
    assert task.cancelled()  # the first cancel still ends the turn as stopped


async def test_turn_status_reads_cancelling_until_a_stopped_turn_has_saved():
    from app.routes.new_chat_routes import _build_turn_status_payload

    log: list[str] = []
    cleanup_started = asyncio.Event()
    never = asyncio.Event()
    page = start_turn_in_background(
        737373,
        _flow_shaped_turn(log, body=never, cleanup_started=cleanup_started),
    )
    assert await anext(page) == "working"

    stopping = asyncio.create_task(stop_running_turn("737373", wait_seconds=2))
    await cleanup_started.wait()
    # Lock released and cancel flag cleared, but the reply is still saving.
    assert _build_turn_status_payload(737373)["status"] == "cancelling"
    await stopping
    assert _build_turn_status_payload(737373) == {"status": "idle"}


# ---------------------------------------------------------------------------
# How a turn ended, for push notifications (relay.on_end, app/push/events.py)
# ---------------------------------------------------------------------------

from app.tasks.chat.streaming.detached_turn.relay import (  # noqa: E402
    TurnEnd,
    frame_type,
)

INTERRUPT = 'data: {"type":"data-interrupt-request","data":{"interrupt_id":"i1"}}\n\n'
ERROR = 'data: {"type":"error","message":"The model is unavailable."}\n\n'
TEXT = 'data: {"type":"text-delta","id":"t","delta":"an error frame"}\n\n'


def test_frame_type_reads_only_the_approval_and_error_frames():
    assert frame_type(INTERRUPT) == "data-interrupt-request"
    assert frame_type(ERROR) == "error"
    assert frame_type(ERROR.encode()) == "error"
    assert frame_type(TEXT) is None
    assert frame_type(KEEPALIVE_FRAME) is None
    assert (
        frame_type('data: {"type":"data-terminal-info","data":{"type":"error"}}\n\n')
        is None
    )
    assert frame_type('data: not json "type":"error"\n\n') is None
    assert frame_type('event: x\ndata: {"type":"error"}\n\n') is None


async def _ended(thread_key: str, turn, *, read_page: bool) -> TurnEnd:
    ends: list[TurnEnd] = []
    page = start_turn_in_background(thread_key, turn, on_end=ends.append)
    if read_page:
        with anyio.fail_after(2):
            async for _ in page:
                pass
    else:
        # The response had started reading (as Starlette's always has) when
        # the tab went: closing the page's reader is what a disconnect does.
        await anext(page)
        await page.aclose()
    await _wait_until_finished(thread_key)
    assert len(ends) == 1
    return ends[0]


async def test_a_page_that_read_to_the_end_counts_as_attached():
    async def turn():
        yield TEXT
        yield TEXT

    end = await _ended("end-attached", turn(), read_page=True)
    assert end.attached is True
    assert not end.failed and not end.cancelled and not end.interrupted
    assert end.seconds >= 0


async def test_a_page_that_left_counts_as_detached():
    async def turn():
        yield TEXT
        await asyncio.sleep(0.02)
        yield TEXT

    end = await _ended("end-detached", turn(), read_page=False)
    assert end.attached is False
    assert not end.failed


async def test_an_approval_request_and_an_error_frame_are_noticed():
    async def paused():
        yield TEXT
        yield INTERRUPT

    async def broke():
        yield TEXT
        yield ERROR

    assert (await _ended("end-paused", paused(), read_page=False)).interrupted is True
    assert (await _ended("end-broke", broke(), read_page=False)).failed is True


async def test_a_turn_that_raises_ended_failed_and_a_stopped_one_cancelled():
    async def raises():
        yield TEXT
        raise RuntimeError("escaped the flow")

    end = await _ended("end-raised", raises(), read_page=False)
    assert end.failed is True and end.cancelled is False

    ends: list[TurnEnd] = []
    started = asyncio.Event()

    async def forever():
        yield TEXT
        started.set()
        await asyncio.Event().wait()

    page = start_turn_in_background("end-stopped", forever(), on_end=ends.append)
    assert await anext(page) == TEXT
    await started.wait()
    assert await stop_running_turn("end-stopped", wait_seconds=2) is True
    assert ends and ends[0].cancelled is True and ends[0].failed is False


async def test_an_on_end_that_raises_changes_nothing_for_the_turn():
    def bad(end: TurnEnd) -> None:
        raise ValueError("push is broken")

    async def turn():
        yield TEXT

    page = start_turn_in_background("end-bad-hook", turn(), on_end=bad)
    with anyio.fail_after(2):
        assert [frame async for frame in page] == [TEXT]
    await _wait_until_finished("end-bad-hook")


def test_the_streaming_services_own_frames_are_recognised():
    # The real writers use json.dumps' default separators ('"type": "error"'),
    # not the compact frames above: a failed turn must still read as failed.
    from app.services.new_streaming_service import VercelStreamingService

    service = VercelStreamingService()
    error = service.format_error("The model is unavailable.", error_code="X")
    assert '"type": "error"' in error
    assert frame_type(error) == "error"
    assert frame_type(error.encode()) == "error"
    interrupt = service.format_interrupt_request(
        {"action_requests": [], "review_configs": []}
    )
    assert frame_type(interrupt) == "data-interrupt-request"
    said = service.format_text_delta("t", 'He wrote {"type": "error"} in a reply')
    assert frame_type(said) is None


async def test_a_turn_that_wrote_the_services_error_frame_ended_failed():
    from app.services.new_streaming_service import VercelStreamingService

    service = VercelStreamingService()

    async def broke():
        yield service.format_text_delta("t", "Looking")
        yield service.format_error("The model is unavailable.")

    assert (await _ended("end-broke-real", broke(), read_page=False)).failed is True
