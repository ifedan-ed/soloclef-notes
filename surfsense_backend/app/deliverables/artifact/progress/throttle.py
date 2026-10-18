"""Which progress updates are worth a database write.

A run can emit an update per streamed token batch and per tool call; the job
row only needs to move when the Studio would show something new. A phase
change is always written. Otherwise progress must have moved far enough, and
enough time must have passed since the last write.
"""

from __future__ import annotations

from .phases import ProgressUpdate

MIN_PROGRESS_DELTA = 3
MIN_INTERVAL_SECONDS = 2.0


class HeartbeatThrottle:
    def __init__(
        self,
        *,
        min_delta: int = MIN_PROGRESS_DELTA,
        min_interval: float = MIN_INTERVAL_SECONDS,
    ) -> None:
        self._min_delta = min_delta
        self._min_interval = min_interval
        self._last: ProgressUpdate | None = None
        self._last_at: float | None = None

    def should_write(self, update: ProgressUpdate, now: float) -> bool:
        last = self._last
        if last is None or update.phase != last.phase:
            return update.progress >= (last.progress if last else 0)
        if update.progress - last.progress < self._min_delta:
            return False
        return self._last_at is None or now - self._last_at >= self._min_interval

    def written(self, update: ProgressUpdate, now: float) -> None:
        self._last = update
        self._last_at = now
