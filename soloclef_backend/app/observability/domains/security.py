"""Security telemetry: auth-failure and rate-limit-rejection metrics."""

from __future__ import annotations

from functools import lru_cache

from app.observability.signals import metrics as m


@lru_cache(maxsize=1)
def _auth_failures():
    return m.get_meter().create_counter(
        "soloclef.auth.failures",
        description="Count of Soloclef authentication failures.",
    )


@lru_cache(maxsize=1)
def _rate_limit_rejections():
    return m.get_meter().create_counter(
        "soloclef.rate_limit.rejections",
        description="Count of Soloclef rate-limit rejections.",
    )


def record_auth_failure(*, reason: str) -> None:
    m.add(_auth_failures(), 1, {"reason": reason})


def record_rate_limit_rejection(*, scope: str) -> None:
    m.add(_rate_limit_rejections(), 1, {"scope": scope})
