"""Run a plan under its run's time limit, watching for someone ending it.

Three ways out:

* ``FINISHED`` -- the plan returned (or raised) on its own;
* ``TIMED_OUT`` -- ``time_left`` ran out first: the plan is cancelled where it
  is, mid-step if need be;
* ``STOPPED`` -- the stop probe said the run was ended from outside (a person
  cancelled it, pausing the automation cancelled it, the overdue sweep timed
  it out, or it was deleted): the plan is cancelled the same way.

The probe is polled every ``STOP_POLL_SECONDS``, so a Cancel takes effect
within about that long. A probe that errors (database hiccup) counts as
"not stopped" and is tried again on the next tick.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from collections.abc import Awaitable, Callable, Coroutine
from dataclasses import dataclass
from enum import StrEnum
from typing import Any

logger = logging.getLogger(__name__)

# Seconds between checks of whether the run was cancelled from outside.
STOP_POLL_SECONDS = 5.0
# Seconds a cancelled plan gets to unwind before it is abandoned.
UNWIND_SECONDS = 30.0

StopProbe = Callable[[], Awaitable[bool]]


class Ending(StrEnum):
    FINISHED = "finished"
    TIMED_OUT = "timed_out"
    STOPPED = "stopped"


@dataclass(frozen=True, slots=True)
class Supervised[T]:
    ending: Ending
    value: T | None = None


async def supervise[T](
    plan: Coroutine[Any, Any, T],
    *,
    time_left: float,
    should_stop: StopProbe | None = None,
    poll_seconds: float | None = None,
) -> Supervised[T]:
    """Await ``plan`` until it finishes, ``time_left`` runs out, or a stop.

    ``poll_seconds`` defaults to ``STOP_POLL_SECONDS``. Re-raises whatever
    ``plan`` raised when it finished by raising.
    """
    if poll_seconds is None:
        poll_seconds = STOP_POLL_SECONDS
    plan_task: asyncio.Task[T] = asyncio.ensure_future(plan)
    watch_task: asyncio.Task[None] | None = (
        asyncio.ensure_future(_wait_for_stop(should_stop, poll_seconds))
        if should_stop is not None
        else None
    )
    waiting: set[asyncio.Future[Any]] = {plan_task}
    if watch_task is not None:
        waiting.add(watch_task)

    try:
        done, _ = await asyncio.wait(
            waiting, timeout=max(0.0, time_left), return_when=asyncio.FIRST_COMPLETED
        )
    except BaseException:
        # The worker itself is being torn down: take both tasks with it.
        await _cancel(plan_task)
        if watch_task is not None:
            await _cancel(watch_task)
        raise

    if plan_task in done:
        if watch_task is not None:
            await _cancel(watch_task)
        return Supervised(Ending.FINISHED, plan_task.result())

    await _cancel(plan_task)
    if watch_task is not None and watch_task in done:
        return Supervised(Ending.STOPPED)
    if watch_task is not None:
        await _cancel(watch_task)
    return Supervised(Ending.TIMED_OUT)


async def _wait_for_stop(should_stop: StopProbe, poll_seconds: float) -> None:
    while True:
        await asyncio.sleep(poll_seconds)
        try:
            if await should_stop():
                return
        except asyncio.CancelledError:
            raise
        except Exception:
            logger.warning("automation run stop check failed; retrying", exc_info=True)


async def _cancel(task: asyncio.Task[Any]) -> None:
    """Cancel ``task`` and give it ``UNWIND_SECONDS`` to finish unwinding."""
    if task.done():
        with contextlib.suppress(BaseException):
            task.result()
        return
    task.cancel()
    done, _ = await asyncio.wait({task}, timeout=UNWIND_SECONDS)
    if task in done:
        with contextlib.suppress(BaseException):
            task.result()
    else:
        logger.warning("automation run step did not stop within %ss", UNWIND_SECONDS)
