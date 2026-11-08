"""Retry policy enforcement for action handlers."""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable


class StepTimeLimitError(TimeoutError):
    """The step's own time limit ran out (not a timeout raised inside the action).

    Kept distinct so a step that ran out of time is recorded as "Timed out",
    while an action's own network timeout stays an ordinary failure.
    """

    def __init__(self, timeout: int) -> None:
        super().__init__(f"took longer than its time limit of {timeout} seconds")
        self.timeout = timeout


async def with_retries[T](
    coro_factory: Callable[[], Awaitable[T]],
    *,
    max_retries: int,
    backoff: str,
    timeout: int | None,
) -> tuple[T, int]:
    """Call ``coro_factory`` up to ``1 + max_retries`` times. Return ``(result, attempts)``.

    Each attempt gets ``timeout`` seconds; one that runs past it raises
    :class:`StepTimeLimitError` and is retried like any other failure.
    """
    total = 1 + max(0, max_retries)
    for attempt in range(1, total + 1):
        try:
            return await _attempt(coro_factory, timeout), attempt
        except Exception:
            if attempt >= total:
                raise
            await asyncio.sleep(_backoff_seconds(backoff, attempt))
    raise RuntimeError("with_retries exhausted without raising or returning")


async def _attempt[T](
    coro_factory: Callable[[], Awaitable[T]], timeout: int | None
) -> T:
    if timeout is None or timeout <= 0:
        return await coro_factory()
    limit = asyncio.timeout(timeout)
    try:
        async with limit:
            return await coro_factory()
    except TimeoutError as exc:
        if limit.expired():
            raise StepTimeLimitError(timeout) from exc
        raise


def _backoff_seconds(strategy: str, attempt: int) -> float:
    if strategy == "exponential":
        return float(2 ** (attempt - 1))
    if strategy == "linear":
        return float(attempt)
    return 0.0
