"""When a temporarily failed email is tried again, and when we stop.

Seven tries over about 45 minutes: long enough to ride out a provider blip or
a rate limit, short enough that a code email is still worth having (and one
whose code expires sooner is dropped at its expiry anyway).
"""

from __future__ import annotations

# Seconds to wait before try 2, 3, ... after a temporary failure.
RETRY_DELAYS_SECONDS = (5, 15, 60, 180, 600, 1800)
MAX_ATTEMPTS = len(RETRY_DELAYS_SECONDS) + 1


def retry_delay(attempts_made: int) -> float | None:
    """Delay before the next try after ``attempts_made`` failures; None to stop."""
    if attempts_made < 1 or attempts_made >= MAX_ATTEMPTS:
        return None
    return float(RETRY_DELAYS_SECONDS[attempts_made - 1])
