"""Launch a run for a trigger that fired: resolve, validate, admit, persist, enqueue.

The trigger-facing entry every selector calls. A selector builds the runtime
inputs and hands one trigger row here; this resolves and guards its automation,
applies "If already running" (``app/automations/concurrency/admission.py``)
under the automation's row lock, snapshots the definition onto a PENDING run,
and enqueues execution. The snapshot makes the run immune to later edits of
the automation.
"""

from __future__ import annotations

import logging
from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.concurrency.admission import Admission, admit_new_run
from app.automations.concurrency.lock import lock_automation
from app.automations.persistence.enums.automation_status import AutomationStatus
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.run import AutomationRun
from app.automations.persistence.models.trigger import AutomationTrigger
from app.automations.runtime import repository
from app.automations.schemas.definition.envelope import AutomationDefinition

from .enqueue import enqueue_run
from .errors import DispatchError
from .inputs import prepare_inputs
from .resolve import resolve_active_automation

logger = logging.getLogger(__name__)


async def launch_run(
    *,
    session: AsyncSession,
    trigger: AutomationTrigger,
    runtime_inputs: dict[str, Any] | None = None,
) -> AutomationRun | None:
    """Resolve ``trigger``'s active automation and enqueue a PENDING run for it.

    Returns ``None`` when "If already running" drops the fire (logged here).
    """
    automation = await resolve_active_automation(session, trigger)

    try:
        definition = AutomationDefinition.model_validate(automation.definition)
    except Exception as exc:
        raise DispatchError(f"invalid automation definition: {exc}") from exc

    inputs = prepare_inputs(definition, trigger, runtime_inputs)
    snapshot = definition.model_dump(mode="json", by_alias=True)
    policy = definition.execution.concurrency

    # Decide and create under the row lock: of two fires at once, the second
    # waits here and then sees the first one's run.
    status = await lock_automation(session, automation.id)
    if status != AutomationStatus.ACTIVE:
        await session.rollback()
        state = status.value if status else "gone"
        raise DispatchError(f"automation {automation.id} is {state}, not active")
    admission = await admit_new_run(session, automation.id, policy)
    if admission is not Admission.CREATE:
        await session.commit()
        logger.info(
            "automation %d: fire from trigger %d dropped (%s; If already running=%s)",
            automation.id,
            trigger.id,
            admission.value,
            policy,
        )
        return None

    run = AutomationRun(
        automation_id=automation.id,
        trigger_id=trigger.id,
        status=RunStatus.PENDING,
        definition_snapshot=snapshot,
        inputs=inputs,
        step_results=[],
        artifacts=[],
    )
    session.add(run)
    await session.commit()
    await session.refresh(run)

    try:
        enqueue_run(run.id, timeout_seconds=definition.execution.timeout_seconds)
    except Exception:
        # Never leave a Pending run no worker will pick up: under "Skip if
        # already running" it would block every later fire.
        await repository.finish(
            session,
            run.id,
            RunStatus.FAILED,
            error={
                "message": "The run could not be handed to a worker.",
                "type": "EnqueueFailed",
            },
            from_statuses=(RunStatus.PENDING,),
        )
        await session.commit()
        raise
    return run
