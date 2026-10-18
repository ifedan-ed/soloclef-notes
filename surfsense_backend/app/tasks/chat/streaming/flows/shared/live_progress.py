"""Save a running turn's steps to its assistant row while it runs.

Only the page that started a turn reads its live stream. Every other page (a
second tab, the phone, the same page after a reload) reads the assistant row
through Zero sync, and that row used to stay empty until the turn finished,
so those pages showed a blank reply for minutes. Saving the turn's parts as
they change lets them show which steps ran, the one running now and the time
so far. Finalize still writes the complete answer at the end.

Automatic behaviour (documented in ``custom/docs/chat-turns.md``):

- The parts are looked at no more than once every
  ``PROGRESS_SAVE_INTERVAL_SECONDS`` and saved only when a step, text or tool
  call changed since the last save.
- A change that lands inside that window is saved when the window ends, even
  if no further frame comes (a specialist's model call streams no frames, a
  page fetch can run for a minute).
- When a tool call starts or ends (the main agent's or a specialist's), the
  parts are looked at as soon as the turn next waits, without waiting for the
  window, so other pages show the new step at once.
- Each save runs in a task of its own: a Stop that lands mid-save cancels the
  turn, not the write, and ``aclose`` (called before finalize) waits for
  writes still going, so none lands after the final answer.
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from collections.abc import Awaitable, Callable, Coroutine
from typing import Any

from sqlalchemy import update

from app.db import NewChatMessage, shielded_async_session
from app.tasks.chat.streaming.shared.stream_result import StreamResult

logger = logging.getLogger(__name__)

PROGRESS_SAVE_INTERVAL_SECONDS = 3.0

WriteProgress = Callable[[int, list[dict[str, Any]]], Awaitable[None]]
Sleep = Callable[[float], Awaitable[None]]


async def _write_progress(message_id: int, parts: list[dict[str, Any]]) -> None:
    async with shielded_async_session() as session:
        await session.execute(
            update(NewChatMessage)
            .where(NewChatMessage.id == message_id)
            .values(content=parts)  # new_chat_messages has no updated_at column
        )
        await session.commit()


def _without_timing(parts: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """The parts minus the clock, which changes on every look."""
    out: list[dict[str, Any]] = []
    for part in parts:
        if part.get("type") == "data-activities":
            data = {k: v for k, v in (part.get("data") or {}).items() if k != "timing"}
            out.append({**part, "data": data})
        else:
            out.append(part)
    return out


def _set_timing(parts: list[dict[str, Any]], timing: dict[str, Any]) -> None:
    for part in parts:
        if part.get("type") == "data-activities":
            part["data"] = {**(part.get("data") or {}), "timing": timing}
            return
    parts.insert(
        0, {"type": "data-activities", "data": {"activities": [], "timing": timing}}
    )


def _can_save(turn: StreamResult) -> bool:
    return (
        turn.content_builder is not None
        and bool(turn.assistant_message_id)
        and turn.activity_timer.status == "running"
    )


def _tool_edges(turn: StreamResult) -> int:
    """How many tool calls have started or ended in the turn's agent stream."""
    return getattr(turn.activity_state, "tool_runs_started_or_ended", 0)


class LiveProgressSaver:
    """Throttled writer of one turn's in-progress parts."""

    def __init__(
        self,
        *,
        interval_seconds: float = PROGRESS_SAVE_INTERVAL_SECONDS,
        clock: Callable[[], float] = time.monotonic,
        write: WriteProgress = _write_progress,
        sleep: Sleep = asyncio.sleep,
    ) -> None:
        self._interval = interval_seconds
        self._clock = clock
        self._write = write
        self._sleep = sleep
        self._turn: StreamResult | None = None
        self._last_checked_at: float | None = None
        self._last_saved_key: str | None = None
        self._seen_tool_edges = 0
        # A look booked for later that has not started yet, and when it is due.
        self._later: asyncio.Task[None] | None = None
        self._later_due: float | None = None
        self._background: set[asyncio.Task[Any]] = set()
        # Writes land in the order their parts were copied.
        self._write_order = asyncio.Lock()
        self._closed = False

    async def maybe_save(self, stream_result: StreamResult) -> bool:
        """Called after every frame: save now, or book a look for later.

        Returns whether a save happened during this call. Never raises: a
        failed save only delays what other pages see.
        """
        if self._closed or not _can_save(stream_result):
            return False
        self._turn = stream_result
        now = self._clock()
        edges = _tool_edges(stream_result)
        if edges != self._seen_tool_edges:
            # A tool call started or ended. Look once this frame's handler is
            # done (when the turn next waits), so the whole step is in.
            self._seen_tool_edges = edges
            self._book_look(now)
            return False
        if (
            self._last_checked_at is not None
            and now - self._last_checked_at < self._interval
        ):
            # Copying and comparing the parts costs as much as a frame, so it
            # happens once per interval; this frame's change is picked up
            # when the interval ends.
            self._book_look(self._last_checked_at + self._interval)
            return False
        return await self._look(now)

    async def aclose(self) -> None:
        """Stop looking and wait for writes still going; call before finalize."""
        self._closed = True
        if self._later is not None:
            self._later.cancel()
            self._later = None
        pending = [task for task in self._background if not task.done()]
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)

    def _book_look(self, due: float) -> None:
        if self._later is not None and not self._later.done():
            if self._later_due is not None and self._later_due <= due:
                return
            self._later.cancel()  # still waiting: a look leaves the slot first
        self._later_due = due
        self._later = self._spawn(self._look_later(due))

    async def _look_later(self, due: float) -> None:
        await self._sleep(max(0.0, due - self._clock()))
        if self._later is asyncio.current_task():
            self._later = None
            self._later_due = None
        await self._look(self._clock())

    async def _look(self, now: float) -> bool:
        turn = self._turn
        if turn is None or self._closed or not _can_save(turn):
            return False
        self._last_checked_at = now
        if self._later is not None and self._later is not asyncio.current_task():
            # This look covers the change the booked one was waiting for.
            self._later.cancel()
            self._later = None
            self._later_due = None

        parts = turn.content_builder.snapshot()
        key = json.dumps(_without_timing(parts), sort_keys=True, default=str)
        if key == self._last_saved_key:
            return False
        # The saved clock lets other pages start counting from the right value.
        _set_timing(parts, dict(turn.activity_timer.snapshot()))
        self._last_saved_key = key
        write = self._spawn(self._write_in_order(turn.assistant_message_id, parts, key))
        # Shielded: a Stop landing now cancels the turn, not the write, which
        # ``aclose`` waits for before finalize writes the answer.
        return await asyncio.shield(write)

    async def _write_in_order(
        self, message_id: int, parts: list[dict[str, Any]], key: str
    ) -> bool:
        async with self._write_order:
            try:
                await self._write(message_id, parts)
            except Exception:
                logger.warning(
                    "[live_progress] save failed (message_id=%s)",
                    message_id,
                    exc_info=True,
                )
                if self._last_saved_key == key:
                    self._last_saved_key = None
                return False
            return True

    def _spawn[T](self, work: Coroutine[Any, Any, T]) -> asyncio.Task[T]:
        task = asyncio.create_task(work)
        self._background.add(task)
        task.add_done_callback(self._background.discard)
        return task
