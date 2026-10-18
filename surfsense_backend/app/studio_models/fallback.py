"""Try the fallback model once when the primary model fails a Studio run.

Only a model failure moves a run to the fallback: a provider error, timeout
or rate limit, a reply that stayed unparseable after ``invoke_json``'s retry,
or a subagent that ended without a saved artifact. A cancellation, a quota or
billing denial, or a render/verification fault does not, because another
model would not change the outcome and a denial must never be retried on a
different bill.
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import Awaitable, Callable
from typing import Any

from app.services.billable_calls import BillingSettlementError, QuotaInsufficientError
from app.utils.structured_output import StructuredOutputError

from .catalog import StudioModel
from .resolution import StudioPlan

logger = logging.getLogger(__name__)

_PROVIDER_MODULES = (
    "litellm",
    "openai",
    "anthropic",
    "httpx",
    "httpcore",
    "aiohttp",
    "google",
)
# SurfSense's own credit denial ("Out of credits for artifact generation") and a
# provider refusing on billing grounds. A plain "quota" is left out on purpose:
# per-minute rate-limit replies say it too, and those should fall back.
_DENIAL_MARKERS = (
    "out of credits",
    "insufficient credit",
    "insufficient_quota",
    "payment required",
    "billing",
)
# Raised by the model's own run rather than by the provider, but still the
# model failing to produce: named, since importing the executors here would
# pull the sandbox and Remotion stack into every importer.
_MODEL_FAILURE_NAMES = {"NoArtifactProducedError", "StudioModelUnavailableError"}
_NEVER_NAMES = {
    "DeliverableJobCancellationError",
    "_SupersededAttemptError",
    "SoftTimeLimitExceeded",
}


class StudioModelUnavailableError(RuntimeError):
    """A resolved model could not be loaded when the run started."""


def _chain(exc: BaseException) -> list[BaseException]:
    seen: set[int] = set()
    chain: list[BaseException] = []
    current: BaseException | None = exc
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        chain.append(current)
        current = current.__cause__
    return chain


def should_fall_back(exc: BaseException) -> bool:
    """Whether ``exc`` is the primary model failing, not the run being stopped."""
    chain = _chain(exc)
    names = {cls.__name__ for item in chain for cls in type(item).__mro__}
    if names & _NEVER_NAMES or any(
        isinstance(
            item,
            asyncio.CancelledError | QuotaInsufficientError | BillingSettlementError,
        )
        for item in chain
    ):
        return False
    text = " ".join(str(item).lower() for item in chain)
    if any(marker in text for marker in _DENIAL_MARKERS):
        return False
    if names & _MODEL_FAILURE_NAMES:
        return True
    for item in chain:
        if isinstance(item, StructuredOutputError | TimeoutError):
            return True
        module = type(item).__module__.split(".", 1)[0]
        if module in _PROVIDER_MODULES:
            return True
    return False


def generated_by(
    plan: StudioPlan, served: str, *, fell_back: bool, primary_error: str | None
) -> dict[str, Any]:
    """What the Studio list shows about the model behind an output."""
    info: dict[str, Any] = {"model": served, "setting": plan.source}
    if fell_back:
        info["fallback"] = True
        info["primary"] = plan.primary.model_id if plan.primary else None
        info["primary_error"] = primary_error
    return info


async def run_with_fallback[T](
    plan: StudioPlan,
    attempt: Callable[[StudioModel | None], Awaitable[tuple[T, str]]],
    *,
    before_fallback: Callable[[], Awaitable[None]] | None = None,
) -> tuple[T, dict[str, Any]]:
    """Run ``attempt`` on the primary, then once on the fallback if it failed.

    ``attempt`` returns its result and the name of the model that served it.
    ``before_fallback`` resets whatever the failed attempt left behind (a
    rolled-back session, say) before the second try.
    """
    try:
        result, served = await attempt(plan.primary)
        return result, generated_by(plan, served, fell_back=False, primary_error=None)
    except Exception as exc:
        if plan.fallback is None or not should_fall_back(exc):
            raise
        logger.warning(
            "Studio model %s failed (%s); retrying once on %s",
            plan.primary.model_id if plan.primary else "auto",
            type(exc).__name__,
            plan.fallback.model_id,
        )
        if before_fallback is not None:
            await before_fallback()
        result, served = await attempt(plan.fallback)
        return result, generated_by(
            plan, served, fell_back=True, primary_error=type(exc).__name__
        )
