"""Execute one plan step: when-predicate, params render, handler dispatch, retries."""

from __future__ import annotations

from collections.abc import Mapping
from datetime import UTC, datetime
from typing import Any

from app.automations.actions import get_action
from app.automations.actions.types import ActionContext
from app.automations.schemas.definition.plan_step import PlanStep
from app.automations.templating import evaluate_predicate, render_value

from .retries import StepTimeLimitError, with_retries


async def execute_step(
    *,
    step: PlanStep,
    template_context: Mapping[str, Any],
    action_context: ActionContext,
    default_max_retries: int,
    default_retry_backoff: str,
    default_timeout_seconds: int,
) -> dict[str, Any]:
    """Run one step and return its structured result entry."""
    started_at = datetime.now(UTC)

    if step.when is not None:
        try:
            should_run = evaluate_predicate(step.when, template_context)
        except Exception as exc:
            return _result(
                step, "failed", started_at, attempts=0, error=_error(exc, "when")
            )
        if not should_run:
            return _result(step, "skipped", started_at, attempts=0)

    try:
        resolved_params = render_value(step.params, template_context)
    except Exception as exc:
        return _result(
            step, "failed", started_at, attempts=0, error=_error(exc, "render")
        )

    action = get_action(step.action)
    if action is None:
        return _result(
            step,
            "failed",
            started_at,
            attempts=0,
            error={
                "message": f"action not registered: {step.action}",
                "type": "ActionNotFound",
            },
        )

    handler = action.build_handler(action_context)

    max_retries = (
        step.max_retries if step.max_retries is not None else default_max_retries
    )
    timeout = step.timeout_seconds or default_timeout_seconds

    try:
        result, attempts = await with_retries(
            lambda: handler(resolved_params),
            max_retries=max_retries,
            backoff=default_retry_backoff,
            timeout=timeout,
        )
    except StepTimeLimitError as exc:
        # Ran out of time on its last try: the run is "Timed out", not "Failed".
        return _result(
            step,
            "timed_out",
            started_at,
            attempts=max_retries + 1,
            error={"message": f"This task {exc}.", "type": "StepTimedOut"},
        )
    except Exception as exc:
        return _result(
            step, "failed", started_at, attempts=max_retries + 1, error=_error(exc)
        )

    reported = _reported_outcome(result)
    if reported is not None:
        status, reason = reported
        return _result(
            step,
            status,
            started_at,
            attempts=attempts,
            result=result,
            error={
                "message": reason
                or (
                    "The agent could not do this task."
                    if status == "failed"
                    else "This step needs your attention."
                ),
                "type": "AgentReportedFailure"
                if status == "failed"
                else "NeedsAttention",
            },
        )
    return _result(step, "succeeded", started_at, attempts=attempts, result=result)


def _reported_outcome(result: Any) -> tuple[str, str | None] | None:
    """``(status, reason)`` when the action says it did not do its task.

    An action reports through ``result["outcome"]`` (the agent task does); a
    handler that returns normally but reports ``failed`` or
    ``needs_attention`` is not a success.
    """
    if not isinstance(result, dict):
        return None
    outcome = result.get("outcome")
    if not isinstance(outcome, dict):
        return None
    status = outcome.get("status")
    if status not in ("failed", "needs_attention"):
        return None
    reason = outcome.get("reason")
    return status, reason if isinstance(reason, str) else None


def interrupted_result(
    step: PlanStep,
    status: str,
    started_at: datetime,
    *,
    error: dict[str, Any],
) -> dict[str, Any]:
    """Entry for a step cut short from outside (run time limit, cancel).

    ``status`` is ``timed_out`` or ``cancelled``; ``attempts`` is left out
    because the step never got to report how many it made.
    """
    entry = _result(step, status, started_at, attempts=0, error=error)
    del entry["attempts"]
    return entry


def _result(
    step: PlanStep,
    status: str,
    started_at: datetime,
    *,
    attempts: int,
    result: Any = None,
    error: dict[str, Any] | None = None,
) -> dict[str, Any]:
    entry: dict[str, Any] = {
        "step_id": step.step_id,
        "action": step.action,
        "status": status,
        "started_at": started_at.isoformat(),
        "finished_at": datetime.now(UTC).isoformat(),
        "attempts": attempts,
    }
    if result is not None:
        entry["result"] = result
    if error is not None:
        entry["error"] = error
    return entry


def _error(exc: Exception, phase: str | None = None) -> dict[str, Any]:
    msg = f"{phase}: {exc}" if phase else str(exc)
    return {"message": msg, "type": type(exc).__name__}
