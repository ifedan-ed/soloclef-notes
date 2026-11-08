"""How long one synthesis attempt may take, and how many are allowed.

A segment of speech is a few sentences, so a minute is already generous for
one; the deadline exists because the provider behind the proxy may not answer
at all. Kokoro's speech route at OpenRouter has been reported to hang for
hours, and the renderer waits on every segment, so an unbounded attempt does
not fail a job -- it holds the queue.

Attempts are counted here rather than delegated to the client library so that
the cap is a number an operator can see and change, and so that the total time
a segment can take is the product of two configured values rather than a
library default.
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass

import httpx

from .errors import TextToSpeechError

logger = logging.getLogger(__name__)

# Reaching the provider should be immediate; only the answer is slow.
_CONNECT_TIMEOUT_SECONDS = 10.0
# Enough for a provider to clear a rate limit without stalling the render.
_BACKOFF_SECONDS = 2.0

# Upstream states that pass: capacity, rate limits, gateway faults. Anything
# else (a rejected voice, an unknown model, a bad key) fails on the first try.
_TRANSIENT_STATUS = frozenset({408, 409, 425, 429, 500, 502, 503, 504, 529})
# Provider libraries raise their own classes for the same conditions, and the
# exception carries no status code when the request never got an answer.
_TRANSIENT_EXCEPTION_NAMES = frozenset(
    {
        "APIConnectionError",
        "APIError",
        "BadGatewayError",
        "InternalServerError",
        "RateLimitError",
        "ServiceUnavailableError",
        "Timeout",
    }
)


class TransientSynthesisError(TextToSpeechError):
    """A fault another attempt could plausibly answer differently."""


@dataclass(frozen=True, slots=True)
class AttemptPolicy:
    """The deadline for one attempt and how many attempts a segment gets."""

    request_timeout_seconds: float
    max_attempts: int
    backoff_seconds: float = _BACKOFF_SECONDS

    @property
    def timeout(self) -> httpx.Timeout:
        """The deadline as the provider libraries express one."""
        return httpx.Timeout(
            connect=_CONNECT_TIMEOUT_SECONDS,
            read=self.request_timeout_seconds,
            write=_CONNECT_TIMEOUT_SECONDS,
            pool=_CONNECT_TIMEOUT_SECONDS,
        )

    @property
    def worst_case_seconds(self) -> float:
        """The longest a segment can take, for the hard stop around it."""
        backoff = self.backoff_seconds * sum(range(self.max_attempts))
        return self.request_timeout_seconds * self.max_attempts + backoff


def load_attempt_policy() -> AttemptPolicy:
    """Read the policy from configuration, rejecting a value it cannot use."""
    from app.config import config as app_config

    return AttemptPolicy(
        request_timeout_seconds=_positive_number(
            app_config.TTS_REQUEST_TIMEOUT_SECONDS, "TTS_REQUEST_TIMEOUT_SECONDS"
        ),
        max_attempts=int(
            _positive_number(app_config.TTS_MAX_ATTEMPTS, "TTS_MAX_ATTEMPTS")
        ),
    )


async def with_attempts[T](
    policy: AttemptPolicy,
    attempt: Callable[[], Awaitable[T]],
    *,
    describe: str,
) -> T:
    """Run ``attempt`` until it succeeds, is not worth retrying, or runs out.

    The whole sequence sits under a hard stop as well as the per-attempt
    deadline, because a client that never returns would otherwise be bounded
    by nothing this code controls.
    """
    try:
        async with asyncio.timeout(policy.worst_case_seconds):
            return await _attempts(policy, attempt, describe=describe)
    except TimeoutError as exc:
        raise TextToSpeechError(
            f"{describe} gave up after {policy.worst_case_seconds:.0f}s without "
            "an answer from the provider"
        ) from exc


async def _attempts[T](
    policy: AttemptPolicy,
    attempt: Callable[[], Awaitable[T]],
    *,
    describe: str,
) -> T:
    last: Exception | None = None
    for number in range(1, policy.max_attempts + 1):
        if number > 1:
            await asyncio.sleep(policy.backoff_seconds * (number - 1))
        try:
            return await attempt()
        except Exception as exc:
            if not is_transient(exc):
                raise TextToSpeechError(f"{describe} failed: {exc}") from exc
            logger.info(
                "%s attempt %d of %d failed and will be retried: %s",
                describe,
                number,
                policy.max_attempts,
                exc,
            )
            last = exc
    raise TextToSpeechError(
        f"{describe} failed after {policy.max_attempts} attempts: {last}"
    ) from last


def is_transient(error: Exception) -> bool:
    """Whether another attempt could plausibly answer differently."""
    if isinstance(error, TransientSynthesisError):
        return True
    status = getattr(error, "status_code", None) or getattr(
        getattr(error, "response", None), "status_code", None
    )
    if isinstance(status, int):
        return status in _TRANSIENT_STATUS
    if isinstance(error, httpx.TimeoutException | httpx.TransportError):
        return True
    return type(error).__name__ in _TRANSIENT_EXCEPTION_NAMES


def _positive_number(value: object, name: str) -> float:
    try:
        number = float(value)  # type: ignore[arg-type]
    except (TypeError, ValueError):
        raise TextToSpeechError(f"{name} must be a number, not {value!r}") from None
    if number <= 0:
        raise TextToSpeechError(f"{name} must be greater than zero, not {number}")
    return number
