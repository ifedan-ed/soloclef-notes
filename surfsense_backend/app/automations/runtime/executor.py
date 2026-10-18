"""Walk an ``AutomationRun``'s snapshot plan to terminal state.

One run, in order:

1. **Claim** (``app/automations/concurrency/start.py``): under the
   automation's row lock the run moves Pending → Running, waits its turn
   ("Queue the next run"), or is Cancelled because the automation was paused.
2. **Plan**, under ``execution.timeout_seconds`` counted from the claim and
   watched for a cancel (``supervise.py``). A step that runs past its own time
   limit (after its retries) ends the run as Timed out; one that fails, as
   Failed; one that needs a person, as Needs attention.
3. **One terminal write**, compare-and-set from Running, and only the run
   that wins it sends the result email ("Email me the result"). When the
   whole run hits its limit the step in progress is cut off, recorded as
   Timed out, and the run is Timed out. A run cancelled from outside already
   carries Cancelled; the executor only stops and records where.
4. **Hand-off**: the oldest queued run of the automation, if any, is given to
   a worker -- also when the message for a run cancelled while it waited
   arrives, in case the Cancel's own hand-off failed.
"""

from __future__ import annotations

import contextlib
import logging
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.actions.types import ActionContext
from app.automations.concurrency.handoff import start_next_waiting_run
from app.automations.concurrency.start import StartClaim, claim_start
from app.automations.notify import send_run_result_email
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun
from app.automations.schemas.definition.envelope import (
    AutomationDefinition,
    AutomationModels,
)
from app.automations.schemas.definition.plan_step import PlanStep
from app.automations.templating import build_run_context
from app.observability.analytics import posthog as ph_analytics
from app.push import events as push_events

from . import repository
from .step import execute_step, interrupted_result
from .supervise import Ending, StopProbe, supervise
from .time_limit import run_timed_out_error

logger = logging.getLogger(__name__)

# Step entry written for the step a cancel or the overdue sweep cut off.
_STOPPED_STEP_ERRORS: dict[RunStatus, dict[str, str]] = {
    RunStatus.CANCELLED: {
        "type": "Cancelled",
        "message": "Stopped here: the run was cancelled.",
    },
    RunStatus.TIMED_OUT: {
        "type": "TimedOut",
        "message": "Stopped here: the run reached its time limit.",
    },
}


@dataclass(frozen=True, slots=True)
class _RunFacts:
    """What the end of a run needs, read up front.

    A cut-off step leaves the session rolled back and the ORM objects expired;
    these plain values stay readable.
    """

    run_id: int
    automation_id: int
    automation_name: str | None
    owner_user_id: UUID | None
    workspace_id: int | None
    trigger_type: str | None


@dataclass(frozen=True, slots=True)
class _PlanOutcome:
    status: RunStatus
    error: dict[str, Any] | None = None
    last_result: dict[str, Any] | None = None


class _Progress:
    """The step in flight, so a time limit or a cancel can say where it stopped."""

    def __init__(self) -> None:
        self.step: PlanStep | None = None
        self.started_at: datetime | None = None

    def begin(self, step: PlanStep) -> None:
        self.step = step
        self.started_at = datetime.now(UTC)

    def end(self) -> None:
        self.step = None
        self.started_at = None


def _facts(run: AutomationRun) -> _RunFacts:
    automation = run.automation
    trigger = run.trigger
    return _RunFacts(
        run_id=run.id,
        automation_id=run.automation_id,
        automation_name=automation.name if automation else None,
        owner_user_id=automation.created_by_user_id if automation else None,
        workspace_id=automation.workspace_id if automation else None,
        trigger_type=trigger.type.value if trigger else None,
    )


def _capture_run_outcome(facts: _RunFacts, status: str) -> None:
    """Emit ``automation_run_completed`` — headless runs the frontend never sees.

    No-op when PostHog is unconfigured or the owning user is unknown.
    """
    if not facts.owner_user_id:
        return
    workspace_id = facts.workspace_id
    ph_analytics.capture(
        "automation_run_completed",
        distinct_id=str(facts.owner_user_id),
        properties={
            "automation_id": facts.automation_id,
            "run_id": facts.run_id,
            "workspace_id": workspace_id,
            "status": status,
            "trigger_type": facts.trigger_type,
        },
        groups={"workspace": str(workspace_id)} if workspace_id is not None else None,
    )


async def execute_run(
    session: AsyncSession,
    run_id: int,
    *,
    should_stop: StopProbe | None = None,
) -> None:
    """Load run ``run_id`` and execute its snapshot plan to a terminal state.

    ``should_stop`` is polled while the plan runs (see ``supervise.py``); the
    Celery task passes one that reads the run's status on its own session.
    Without it a cancel is not noticed until the run ends.
    """
    run = await repository.load_run(session, run_id)
    if run is None:
        raise ValueError(f"automation_run {run_id} not found")

    if run.status != RunStatus.PENDING:
        if run.status == RunStatus.CANCELLED:
            await _pass_queue_on_after_cancel(session, run.automation_id)
        return
    facts = _facts(run)

    try:
        definition = AutomationDefinition.model_validate(run.definition_snapshot)
    except Exception as exc:
        failed = await repository.finish(
            session,
            run.id,
            RunStatus.FAILED,
            error={
                "message": f"definition_snapshot invalid: {exc}",
                "type": type(exc).__name__,
            },
            from_statuses=(RunStatus.PENDING,),
        )
        await session.commit()
        if failed:
            _capture_run_outcome(facts, "failed")
        return

    claim = await claim_start(session, run, definition.execution.concurrency)
    if claim is not StartClaim.STARTED:
        logger.info("automation_run %d not started: %s", run_id, claim.value)
        if (
            claim is StartClaim.GONE
            and await _status_or_none(session, run_id) == RunStatus.CANCELLED
        ):
            await _pass_queue_on_after_cancel(session, facts.automation_id)
        return

    timeout = definition.execution.timeout_seconds
    progress = _Progress()
    try:
        supervised = await supervise(
            _walk_plan(session, run, definition, progress),
            time_left=timeout,
            should_stop=should_stop,
        )
    except Exception as exc:
        await _settle(session)
        if await _status_or_none(session, run_id) is None:
            logger.info("automation_run %d was deleted while running", run_id)
            return
        await _end(
            session,
            facts,
            definition,
            RunStatus.FAILED,
            error={
                "message": f"The run stopped on an internal error ({type(exc).__name__}).",
                "type": type(exc).__name__,
            },
        )
        await start_next_waiting_run(session, facts.automation_id)
        raise

    if supervised.ending is Ending.FINISHED:
        outcome = supervised.value
        assert outcome is not None
        await _end(
            session,
            facts,
            definition,
            outcome.status,
            error=outcome.error,
            result=outcome.last_result,
        )
    elif supervised.ending is Ending.TIMED_OUT:
        error = run_timed_out_error(timeout)
        await _settle(session)
        await _record_stopped_step(session, facts, progress, RunStatus.TIMED_OUT, error)
        await _end(session, facts, definition, RunStatus.TIMED_OUT, error=error)
    else:
        # Ended from outside: whoever ended it already wrote its status.
        await _settle(session)
        ended_as = await _status_or_none(session, run_id)
        if ended_as in _STOPPED_STEP_ERRORS:
            await _record_stopped_step(
                session, facts, progress, ended_as, _STOPPED_STEP_ERRORS[ended_as]
            )
        logger.info(
            "automation_run %d stopped from outside (%s)",
            run_id,
            ended_as.value if ended_as else "deleted",
        )

    await start_next_waiting_run(session, facts.automation_id)


async def _pass_queue_on_after_cancel(
    session: AsyncSession, automation_id: int
) -> None:
    """The message of a run cancelled while it waited has arrived: wake the next.

    Cancel hands the queue on itself (``services/run.py``); this covers that
    hand-off failing, so the next queued run doesn't wait for the sweep.
    Harmless when it already started: the hand-off re-checks under the lock.
    """
    await start_next_waiting_run(session, automation_id)


async def _walk_plan(
    session: AsyncSession,
    run: AutomationRun,
    definition: AutomationDefinition,
    progress: _Progress,
) -> _PlanOutcome:
    """Run the plan's steps in order; stop at the first that does not succeed."""
    step_outputs: dict[str, Any] = {}
    last_result: dict[str, Any] | None = None

    for step in definition.plan:
        result = await _run_step(session, run, step, definition, step_outputs, progress)

        if result["status"] in ("failed", "timed_out"):
            await _run_on_failure(session, run, definition, progress)
            return _PlanOutcome(
                RunStatus.TIMED_OUT
                if result["status"] == "timed_out"
                else RunStatus.FAILED,
                error=result.get("error"),
            )

        if result["status"] == "needs_attention":
            # The rest of the plan would build on an unfinished step; stop and
            # tell the owner what to do instead of calling the run a success.
            return _PlanOutcome(RunStatus.NEEDS_ATTENTION, error=result.get("error"))

        if result["status"] == "succeeded":
            step_outputs[step.output_as or step.step_id] = result.get("result")
            step_result = result.get("result")
            if isinstance(step_result, dict):
                last_result = step_result

    return _PlanOutcome(RunStatus.SUCCEEDED, last_result=last_result)


async def _run_step(
    session: AsyncSession,
    run: AutomationRun,
    step: PlanStep,
    definition: AutomationDefinition,
    step_outputs: dict[str, Any],
    progress: _Progress,
) -> dict[str, Any]:
    progress.begin(step)
    result = await execute_step(
        step=step,
        template_context=_build_template_ctx(run, step_outputs),
        action_context=_build_action_ctx(session, run, step, definition.models),
        default_max_retries=definition.execution.max_retries,
        default_retry_backoff=definition.execution.retry_backoff,
        default_timeout_seconds=definition.execution.timeout_seconds,
    )
    progress.end()
    await repository.append_step_result(session, run, result)
    await session.commit()
    return result


async def _run_on_failure(
    session: AsyncSession,
    run: AutomationRun,
    definition: AutomationDefinition,
    progress: _Progress,
) -> None:
    """Run the on_failure steps. Their failures don't recurse into more on_failure.

    They count against the same run time limit as the plan.
    """
    for step in definition.execution.on_failure:
        await _run_step(session, run, step, definition, {}, progress)


async def _end(
    session: AsyncSession,
    facts: _RunFacts,
    definition: AutomationDefinition,
    status: RunStatus,
    *,
    error: dict[str, Any] | None = None,
    result: dict[str, Any] | None = None,
) -> None:
    """Write the terminal status; only if this call wrote it, report and email."""
    try:
        ended = await repository.finish(session, facts.run_id, status, error=error)
        await session.commit()
    except Exception:
        logger.exception(
            "automation_run %d: could not record %s", facts.run_id, status.value
        )
        await _settle(session)
        return
    if not ended:
        # Cancelled, swept or deleted meanwhile: that writer owns the outcome.
        return
    _capture_run_outcome(facts, status.value)
    # The phone: "An automation finished" / "failed", to its owner (app/push).
    push_events.automation_run_ended(
        owner_user_id=facts.owner_user_id,
        automation_id=facts.automation_id,
        run_id=facts.run_id,
        status=status.value,
    )
    await _maybe_email_result(
        session, facts, definition, status=status.value, result=result, error=error
    )


async def _record_stopped_step(
    session: AsyncSession,
    facts: _RunFacts,
    progress: _Progress,
    status: RunStatus,
    error: dict[str, Any],
) -> None:
    """Add a step entry for the step that was cut off, if one was in flight."""
    if progress.step is None or progress.started_at is None:
        return
    entry = interrupted_result(
        progress.step, status.value, progress.started_at, error=error
    )
    try:
        await repository.append_step_result_by_id(session, facts.run_id, entry)
        await session.commit()
    except Exception:
        logger.warning(
            "automation_run %d: could not record the stopped step",
            facts.run_id,
            exc_info=True,
        )
        await _settle(session)


async def _settle(session: AsyncSession) -> None:
    """Roll back whatever the cut-off step left half done on the session."""
    with contextlib.suppress(Exception):
        await session.rollback()


async def _status_or_none(session: AsyncSession, run_id: int) -> RunStatus | None:
    try:
        return await repository.current_status(session, run_id)
    except Exception:
        await _settle(session)
        return None


def _build_template_ctx(
    run: AutomationRun, step_outputs: dict[str, Any]
) -> dict[str, Any]:
    automation = run.automation
    trigger = run.trigger
    return build_run_context(
        run_id=run.id,
        automation_id=run.automation_id,
        automation_name=automation.name if automation else None,
        automation_version=automation.version if automation else None,
        workspace_id=automation.workspace_id if automation else None,
        creator_id=automation.created_by_user_id if automation else None,
        trigger_id=run.trigger_id,
        trigger_type=trigger.type.value if trigger else None,
        started_at=run.started_at,
        attempt=1,
        inputs=run.inputs or {},
        step_outputs=step_outputs,
    )


async def _maybe_email_result(
    session: AsyncSession,
    facts: _RunFacts,
    definition: AutomationDefinition,
    *,
    status: str,
    result: dict[str, Any] | None = None,
    error: dict[str, Any] | None = None,
) -> None:
    if not definition.execution.email_result:
        return
    if facts.automation_name is None:
        return
    await send_run_result_email(
        session,
        owner_user_id=facts.owner_user_id,
        automation_id=facts.automation_id,
        automation_name=facts.automation_name,
        status=status,
        result=result,
        error=error,
    )


def _build_action_ctx(
    session: AsyncSession,
    run: AutomationRun,
    step: PlanStep,
    models: AutomationModels | None,
) -> ActionContext:
    automation = run.automation
    trigger = run.trigger
    run_timezone = None
    if trigger is not None and isinstance(trigger.params, dict):
        tz = trigger.params.get("timezone")
        if isinstance(tz, str) and tz:
            run_timezone = tz
    return ActionContext(
        session=session,
        run_id=run.id,
        step_id=step.step_id,
        workspace_id=automation.workspace_id,
        creator_user_id=automation.created_by_user_id,
        chat_model_id=models.chat_model_id if models else None,
        image_gen_model_id=models.image_gen_model_id if models else None,
        vision_model_id=models.vision_model_id if models else None,
        automation_name=automation.name if automation else None,
        automation_id=automation.id if automation else None,
        run_timezone=run_timezone,
    )
