"""When a Studio run moves to its fallback model, and what it records."""

from __future__ import annotations

import asyncio
import uuid

import httpx
import litellm
import pytest

from app.services.billable_calls import BillingSettlementError, QuotaInsufficientError
from app.studio_models.catalog import StudioModel
from app.studio_models.fallback import (
    StudioModelUnavailableError,
    run_with_fallback,
    should_fall_back,
)
from app.studio_models.resolution import StudioPlan
from app.utils.structured_output import StructuredOutputError

pytestmark = pytest.mark.unit

LUNA = StudioModel(id=3, model_id="openai-gpt-5.6-luna")
FLASH = StudioModel(id=2, model_id="ds-deepseek-v4.1-flash")


class NoArtifactProducedError(RuntimeError):
    """Stands in for the artifact executor's error, matched by name."""


class DeliverableJobCancellationError(Exception):
    """Stands in for the video executor's cancellation, matched by name."""


def _wrapped(cause: BaseException) -> RuntimeError:
    try:
        raise RuntimeError("the run failed") from cause
    except RuntimeError as exc:
        return exc


def _quota() -> QuotaInsufficientError:
    return QuotaInsufficientError(
        usage_type="queued_deliverable_generation",
        balance_micros=0,
        remaining_micros=0,
    )


@pytest.mark.parametrize(
    "exc",
    [
        litellm.RateLimitError("slow down", llm_provider="openai", model="luna"),
        litellm.Timeout("timed out", model="luna", llm_provider="openai"),
        litellm.ServiceUnavailableError("503", llm_provider="openai", model="luna"),
        litellm.NotFoundError("model gone", llm_provider="openai", model="luna"),
        httpx.ReadTimeout("read timed out"),
        TimeoutError(),
        StructuredOutputError("reply did not match the schema after a retry"),
        _wrapped(StructuredOutputError("bad scenes")),
        _wrapped(litellm.APIConnectionError("reset", llm_provider="openai", model="x")),
        NoArtifactProducedError("no verified artifact was saved"),
        StudioModelUnavailableError("could not be loaded"),
    ],
    ids=lambda exc: type(exc).__name__,
)
def test_a_model_failure_falls_back(exc):
    assert should_fall_back(exc)


@pytest.mark.parametrize(
    "exc",
    [
        DeliverableJobCancellationError(),
        asyncio.CancelledError(),
        _quota(),
        BillingSettlementError(
            usage_type="podcast_generation",
            user_id=uuid.uuid4(),
            cause=litellm.Timeout("timed out", model="luna", llm_provider="openai"),
        ),
        RuntimeError("Out of credits for artifact generation"),
        litellm.RateLimitError(
            "insufficient_quota: check your plan and billing details",
            llm_provider="openai",
            model="luna",
        ),
        _wrapped(_quota()),
        RuntimeError("video verification failed: blank frame"),
        ValueError("video duration 190.0s exceeds the 180-second limit"),
        RuntimeError("Remotion render exited 1"),
    ],
    ids=lambda exc: f"{type(exc).__name__}:{str(exc)[:24]}",
)
def test_a_stop_a_denial_or_a_render_fault_does_not(exc):
    assert not should_fall_back(exc)


async def test_the_primary_serves_when_it_works():
    tried = []

    async def attempt(model):
        tried.append(model)
        return "quiz", model.model_id

    result, info = await run_with_fallback(StudioPlan(LUNA, FLASH, "override"), attempt)

    assert (result, tried) == ("quiz", [LUNA])
    assert info == {"model": LUNA.model_id, "setting": "override"}


async def test_a_failed_primary_retries_once_on_the_fallback_and_says_so():
    tried, resets = [], []

    async def attempt(model):
        tried.append(model)
        if model is LUNA:
            raise litellm.Timeout("timed out", model="luna", llm_provider="openai")
        return "quiz", model.model_id

    async def reset():
        resets.append(True)

    _, info = await run_with_fallback(
        StudioPlan(LUNA, FLASH, "default"), attempt, before_fallback=reset
    )

    assert tried == [LUNA, FLASH] and resets == [True]
    assert info == {
        "model": FLASH.model_id,
        "setting": "default",
        "fallback": True,
        "primary": LUNA.model_id,
        "primary_error": "Timeout",
    }


async def test_the_fallback_is_tried_only_once():
    tried = []

    async def attempt(model):
        tried.append(model)
        raise StructuredOutputError(f"{model.model_id} replied badly")

    with pytest.raises(StructuredOutputError, match="deepseek"):
        await run_with_fallback(StudioPlan(LUNA, FLASH, "default"), attempt)
    assert tried == [LUNA, FLASH]


async def test_no_fallback_on_a_denial_or_without_a_fallback_model():
    async def denied(_model):
        raise _quota()

    with pytest.raises(QuotaInsufficientError):
        await run_with_fallback(StudioPlan(LUNA, FLASH, "default"), denied)

    async def failing(_model):
        raise StructuredOutputError("bad")

    with pytest.raises(StructuredOutputError):
        await run_with_fallback(StudioPlan(LUNA, None, "override"), failing)


async def test_auto_records_the_chat_model_it_ran_on():
    async def attempt(model):
        assert model is None
        return 7, "google-gemini-3.8-flash"

    _, info = await run_with_fallback(StudioPlan(None, None, "auto"), attempt)
    assert info == {"model": "google-gemini-3.8-flash", "setting": "auto"}
