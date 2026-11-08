"""Run a chat turn in the background and relay its frames to the page.

Before this, a turn ran inside the HTTP response: a closed tab, a reload, a
phone putting the browser to sleep or a second Regenerate dropped the
connection, and Starlette cancelled the turn with it. Now the turn runs in its
own task; the response only listens. When the page goes away the turn keeps
going, saves its answer and releases the thread; any page sees it through the
saved progress and the turn-status endpoint, and Stop
(``POST /threads/{id}/cancel-active-turn``) cancels it from anywhere.

Automatic behaviour (documented in ``custom/docs/chat-turns.md``):

- While the page listens and the turn is silent (a slow page fetch, a long
  model call), a comment frame goes out every ``KEEPALIVE_INTERVAL_SECONDS``
  so proxies (Cloudflare drops a response idle for 100 s) keep the stream
  open. Browsers and the mobile app ignore comment frames.
- Frames produced after the page left are dropped; nothing is buffered for a
  page that is gone. A page that comes back reads the saved progress instead.
- When the turn ends, ``on_end`` (if the route gave one) is told how it ended
  and whether a page was still listening: that is how a push notification
  ("Your answer is ready", "Approval needed") knows whether anyone saw it
  (``app/push/events.py``, ``custom/docs/push-notifications.md``). The relay
  reads only two frame types for that, the approval request and the error
  frame, and never the answer's text.
"""

from __future__ import annotations

import asyncio
import json
import logging
import re
import time
from collections.abc import AsyncIterator, Callable
from dataclasses import dataclass

from . import registry

logger = logging.getLogger(__name__)

KEEPALIVE_INTERVAL_SECONDS = 15.0
KEEPALIVE_FRAME = ": keepalive\n\n"

Frame = str | bytes

_END = object()

# The two frames the relay recognises (``new_streaming_service.py``): an
# approval request pauses the turn, and an error frame ends it as failed.
_INTERRUPT_TYPE = "data-interrupt-request"
_ERROR_TYPE = "error"
# The cheap test before a frame is parsed: ``"type": "error"`` with any
# spacing, since the streaming service writes frames with ``json.dumps``'
# default separators (``"type": "error"``) and other writers may not.
_ERROR_MENTION = re.compile(r'"type"\s*:\s*"error"')


@dataclass(frozen=True, slots=True)
class TurnEnd:
    """How a turn ended, for whoever asked to be told (``on_end``)."""

    # A page was still reading the stream when the turn ended.
    attached: bool
    # From the turn's start to its end, in seconds.
    seconds: float
    # The turn wrote an error frame, or raised.
    failed: bool
    # Stop cancelled it.
    cancelled: bool
    # It paused for an approval (a ``data-interrupt-request`` frame).
    interrupted: bool


OnEnd = Callable[[TurnEnd], None]


def frame_type(frame: Frame) -> str | None:
    """The ``type`` of a data frame the relay cares about; None for any other.

    Only frames that mention one of the two types are parsed, so the answer's
    text deltas cost a substring check each and nothing more.
    """
    text = frame.decode("utf-8", "replace") if isinstance(frame, bytes) else frame
    if _INTERRUPT_TYPE not in text and not _ERROR_MENTION.search(text):
        return None
    if not text.startswith("data: "):
        return None
    try:
        payload = json.loads(text[len("data: ") :].strip())
    except ValueError:
        return None
    kind = payload.get("type") if isinstance(payload, dict) else None
    return kind if kind in (_INTERRUPT_TYPE, _ERROR_TYPE) else None


class _Listener:
    """The page reading this turn's frames, while it is still connected."""

    def __init__(self) -> None:
        self.queue: asyncio.Queue[object] = asyncio.Queue()
        self.attached = True

    def offer(self, item: object) -> None:
        if self.attached:
            self.queue.put_nowait(item)

    def detach(self) -> None:
        self.attached = False
        while not self.queue.empty():
            self.queue.get_nowait()


async def _run_turn(
    frames: AsyncIterator[Frame],
    listener: _Listener,
    thread_key: str,
    on_end: OnEnd | None,
) -> None:
    started = time.monotonic()
    failed = cancelled = interrupted = False
    try:
        async for frame in frames:
            kind = frame_type(frame)
            if kind == _INTERRUPT_TYPE:
                interrupted = True
            elif kind == _ERROR_TYPE:
                failed = True
            listener.offer(frame)
    except asyncio.CancelledError:
        cancelled = True
        logger.info("[chat-turn] thread=%s stopped", thread_key)
        raise
    except Exception:
        # The flows turn their own errors into error frames; reaching here
        # means something escaped them. The turn is over either way.
        failed = True
        logger.exception("[chat-turn] thread=%s ended with an error", thread_key)
    finally:
        # Read before the end frame goes out: the page's reader detaches on it.
        attached = listener.attached
        listener.offer(_END)
        if on_end is not None:
            try:
                on_end(
                    TurnEnd(
                        attached=attached,
                        seconds=time.monotonic() - started,
                        failed=failed,
                        cancelled=cancelled,
                        interrupted=interrupted,
                    )
                )
            except Exception:  # a report must never change how the turn ended
                logger.exception("[chat-turn] thread=%s on_end failed", thread_key)


def start_turn_in_background(
    thread_id: int | str,
    frames: AsyncIterator[Frame],
    *,
    keepalive_seconds: float = KEEPALIVE_INTERVAL_SECONDS,
    on_end: OnEnd | None = None,
) -> AsyncIterator[Frame]:
    """Start ``frames`` (a flow's SSE generator) now; return what the page reads.

    The turn starts immediately, before the response is sent, and is listed
    in :mod:`registry` until it has finished saving. ``on_end`` is called once,
    from the turn's own task, when it has ended (``TurnEnd``).
    """
    thread_key = str(thread_id)
    listener = _Listener()
    task = asyncio.create_task(
        _run_turn(frames, listener, thread_key, on_end),
        name=f"chat-turn:{thread_key}",
    )
    registry.register(thread_key, task)
    return _listen(listener, keepalive_seconds)


async def _listen(
    listener: _Listener, keepalive_seconds: float
) -> AsyncIterator[Frame]:
    try:
        while True:
            try:
                item = await asyncio.wait_for(
                    listener.queue.get(), timeout=keepalive_seconds
                )
            except TimeoutError:
                yield KEEPALIVE_FRAME
                continue
            if item is _END:
                return
            yield item  # type: ignore[misc]
    finally:
        # The page left (or the turn ended): stop queueing frames for it. The
        # turn itself is not touched.
        listener.detach()
