"""Run a turn's end-of-turn work so that no Stop can cut it short.

A turn's ``finally`` saves the reply, writes its token usage, releases the
premium credit hold and clears "AI is responding". Stop cancels the turn's
task (``registry.stop``). ``anyio.CancelScope(shield=True)`` does not hold off
a plain ``task.cancel()``: a second cancel, or a first one that arrives while
the turn is already finishing, raised ``CancelledError`` at the next ``await``
of that work and left the reply half-saved, the credit held and the thread
marked busy.

Here the work runs in a task of its own behind ``asyncio.shield``. A cancel
aimed at the turn only interrupts the wait; the wait starts again until the
work is done. Cancels that arrive during the wait are then dropped
(``Task.uncancel``): the turn is already ending, and a ``CancelledError`` that
was already on its way out of the turn when the ``finally`` began still
propagates.
"""

from __future__ import annotations

import asyncio
from collections.abc import Coroutine
from typing import Any

import anyio


async def run_cleanup_to_the_end[T](cleanup: Coroutine[Any, Any, T]) -> T:
    """Await ``cleanup`` to completion, whatever cancels reach this task."""
    work = asyncio.ensure_future(cleanup)
    this_task = asyncio.current_task()
    dropped_cancels = 0
    try:
        # The anyio shield stops an anyio scope above (a caller inside a task
        # group) from re-sending its cancel on every pass of the loop below.
        with anyio.CancelScope(shield=True):
            while not work.done():
                try:
                    await asyncio.shield(work)
                except asyncio.CancelledError:
                    if work.cancelled():
                        # The work itself was cancelled (event-loop shutdown
                        # cancels every task): nothing left to wait for.
                        raise
                    dropped_cancels += 1
    finally:
        if this_task is not None:
            for _ in range(dropped_cancels):
                this_task.uncancel()
    return work.result()
