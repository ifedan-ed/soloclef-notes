"""Celery execution and outbox reconciliation for queued deliverables."""

from __future__ import annotations

import asyncio
import contextlib
import logging
import re
import time
from contextlib import asynccontextmanager
from datetime import UTC, datetime, timedelta
from typing import Any

from billiard.exceptions import SoftTimeLimitExceeded
from sqlalchemy import select

from app.celery_app import celery_app
from app.db import (
    DeliverableFailureCode,
    DeliverableJob,
    DeliverableJobStatus,
    Workspace,
)
from app.deliverables.artifact.executor import (
    artifact_sandbox_owner,
    execute_artifact_deliverable,
)
from app.deliverables.artifact.progress import (
    ArtifactProgressTracker,
    JobProgressHeartbeat,
)
from app.deliverables.jobs.dispatch import (
    DELIVERABLE_JOB_TASK,
    dispatch_deliverable_job,
)
from app.deliverables.jobs.policy import (
    ARTIFACT_KIND,
    VIDEO_KIND,
    VIDEO_SPEC,
    get_deliverable_kind_spec,
)
from app.deliverables.jobs.service import (
    cancel_deliverable_job,
    claim_deliverable_job,
    complete_deliverable_job,
    fail_deliverable_job,
    heartbeat_deliverable_job,
    list_orphaned_running_jobs,
    list_stale_cancelling_jobs,
    list_stale_queued_jobs,
    requeue_claimed_deliverable_job,
    touch_deliverable_job_heartbeat,
)
from app.deliverables.video.executor import (
    DeliverableJobCancellationError,
    execute_video_deliverable,
    video_sandbox_owner,
)
from app.observability.logs import bind_log_context
from app.sandbox import get_registry
from app.services.billable_calls import (
    BillingSettlementError,
    QuotaInsufficientError,
    _resolve_agent_billing_for_workspace,
    billable_call,
)
from app.services.llm_error_adapter import adapt_llm_exception
from app.studio_models.fallback import run_with_fallback
from app.studio_models.llm import studio_llm_bundle
from app.studio_models.origin import plan_for_job
from app.studio_models.provenance import record_for_job
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task
from app.tasks.chat.streaming.flows.new_chat.auto_pin import resolve_initial_auto_pin
from app.tasks.chat.streaming.flows.shared.llm_bundle import load_llm_bundle
from app.utils.structured_output import StructuredOutputError

logger = logging.getLogger(__name__)

RECONCILE_TASK = "deliverables.reconcile_queued"
_OUTBOX_STALE_AFTER = timedelta(minutes=2)
_CANCELLING_STALE_AFTER = timedelta(minutes=5)
# A live attempt refreshes heartbeat_at every _KEEPALIVE_SECONDS from its
# cancellation watcher, whatever phase it is in. Ten silent minutes is ten
# missed keep-alives: the worker is gone (a container restart kills the
# attempt without any state transition), not merely rendering slowly.
_KEEPALIVE_SECONDS = 60.0
_RUNNING_STALE_AFTER = timedelta(minutes=10)
# An attempt whose worker keeps dying (out of memory on every render, say)
# must end rather than loop; the count rides in the job's checkpoint.
_MAX_ORPHAN_REQUEUES = 2
_ORPHAN_REQUEUES_KEY = "orphan_requeues"
_CANCEL_POLL_SECONDS = 0.5
_OUTBOX_BATCH_SIZE = 100
_MAX_PROVIDER_RETRIES = 3
_MAX_DIAGNOSTIC_CHARS = 1200
_SECRET_PATTERN = re.compile(
    r"(?i)(api[_-]?key|authorization|password|secret|token)\s*[:=]\s*\S+"
)
_URL_CREDENTIALS_PATTERN = re.compile(r"(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@")
_TRANSIENT_PROVIDER_EXCEPTION_NAMES = {
    "APIConnectionError",
    "APITimeoutError",
    "BadGatewayError",
    "ConnectError",
    "ConnectTimeout",
    "GatewayTimeoutError",
    "InternalServerError",
    "RateLimitError",
    "ReadTimeout",
    "ServiceUnavailableError",
    "TooManyRequests",
    "TooManyRequestsError",
}


def _sandbox_owner(job: DeliverableJob) -> str:
    """Where a job's sandbox lives: per attempt for video, per thread otherwise."""
    if job.kind == ARTIFACT_KIND:
        return artifact_sandbox_owner(job)
    return video_sandbox_owner(job.id, job.attempt_count)


class _SupersededAttemptError(Exception):
    """Stop an old worker after a retry has advanced the job attempt."""


@asynccontextmanager
async def _celery_billable_session():
    async with get_celery_session_maker()() as session:
        yield session


class _BillableQueuedLLM:
    """Bill only the executor's targeted authoring and repair LLM calls."""

    def __init__(
        self,
        llm,
        *,
        user_id,
        workspace_id: int,
        billing_tier: str,
        base_model: str,
        quota_reserve_tokens: int | None,
        thread_id: int,
        job_id: int,
    ) -> None:
        self._llm = llm
        self._billing = {
            "user_id": user_id,
            "workspace_id": workspace_id,
            "billing_tier": billing_tier,
            "base_model": base_model,
            "quota_reserve_tokens": quota_reserve_tokens,
            "usage_type": "queued_deliverable_generation",
            "thread_id": thread_id,
            "call_details": {"deliverable_job_id": job_id, "kind": VIDEO_KIND},
            "billable_session_factory": _celery_billable_session,
        }

    async def ainvoke(self, *args, **kwargs):
        async with billable_call(**self._billing):
            return await self._llm.ainvoke(*args, **kwargs)

    def __getattr__(self, name: str):
        return getattr(self._llm, name)


def classify_deliverable_failure(
    exc: BaseException,
    *,
    retries: int = 0,
) -> tuple[DeliverableFailureCode, bool]:
    """Map internal exceptions to stable public state and retry eligibility.

    ``retries`` is how many times this job has already been retried.
    """
    if isinstance(exc, QuotaInsufficientError):
        return DeliverableFailureCode.QUOTA_EXCEEDED, False
    if isinstance(exc, BillingSettlementError):
        return DeliverableFailureCode.GENERATION_FAILED, False
    # Before any substring matching: a structured-output failure describes the
    # model's reply (field paths, validator messages, sometimes the reply
    # itself), and for a video that text is full of "render", "Remotion" and
    # "verify". It is the model answering badly, not a render or verification
    # fault. A fresh attempt often succeeds, so it is retried, but only once.
    if _structured_output_failure(exc) is not None:
        return DeliverableFailureCode.GENERATION_FAILED, retries == 0

    message = str(exc).lower()
    if "out of credits" in message or "quota" in message:
        return DeliverableFailureCode.QUOTA_EXCEEDED, False
    if "duration" in message and "limit" in message:
        return DeliverableFailureCode.DURATION_LIMIT, False
    if "verif" in message or "no verified artifact" in message:
        return DeliverableFailureCode.VERIFICATION_FAILED, False
    if isinstance(exc, SoftTimeLimitExceeded) or any(
        marker in message
        for marker in ("remotion", "render", "ffmpeg", "chromium", "chrome")
    ):
        return DeliverableFailureCode.RENDER_FAILED, False

    adaptation = adapt_llm_exception(exc)
    class_names = {cls.__name__ for cls in type(exc).__mro__}
    provider_transient = adaptation.retryable and bool(
        class_names & _TRANSIENT_PROVIDER_EXCEPTION_NAMES
    )
    return DeliverableFailureCode.GENERATION_FAILED, provider_transient


def _structured_output_failure(exc: BaseException) -> StructuredOutputError | None:
    """The StructuredOutputError ``exc`` is or explicitly wraps, if any."""
    seen: set[int] = set()
    current: BaseException | None = exc
    while current is not None and id(current) not in seen:
        if isinstance(current, StructuredOutputError):
            return current
        seen.add(id(current))
        current = current.__cause__
    return None


def sanitize_internal_error(exc: BaseException) -> str:
    """Bound diagnostics and remove common credential forms before persistence."""
    text = " ".join(str(exc).split())
    text = _SECRET_PATTERN.sub(r"\1=[redacted]", text)
    text = _URL_CREDENTIALS_PATTERN.sub(r"\1[redacted]@", text)
    return f"{type(exc).__name__}: {text}"[:_MAX_DIAGNOSTIC_CHARS]


async def _resolve_worker_model(session, job: DeliverableJob):
    workspace = (
        await session.execute(select(Workspace).where(Workspace.id == job.workspace_id))
    ).scalar_one_or_none()
    if workspace is None or workspace.chat_model_id is None:
        raise ValueError("workspace chat model is unavailable")
    if job.thread_id is None:
        raise ValueError("queued deliverable requires a root thread")

    config_id = workspace.chat_model_id
    requesting_user_id = str(job.created_by_id or workspace.user_id)
    if config_id == 0:
        pin = await resolve_initial_auto_pin(
            session,
            chat_id=job.thread_id,
            workspace_id=job.workspace_id,
            user_id=requesting_user_id,
            selected_llm_config_id=0,
            requires_image_input=False,
            requested_llm_config_id=0,
        )
        if pin.error is not None or pin.llm_config_id is None:
            raise ValueError("workspace chat model could not be resolved")
        config_id = pin.llm_config_id

    llm, agent_config, error = await load_llm_bundle(
        session,
        config_id=config_id,
        workspace_id=job.workspace_id,
    )
    if llm is None or agent_config is None or error is not None:
        raise ValueError("workspace chat model could not be loaded")
    return llm, agent_config


async def _finish_failure(
    session,
    job_id: int,
    *,
    failure_code: DeliverableFailureCode,
    diagnostic: str,
    task_id: str | None,
) -> None:
    failed = await fail_deliverable_job(
        session,
        job_id,
        failure_code=failure_code,
        internal_error=diagnostic,
        task_id=task_id,
    )
    if failed is None:
        await cancel_deliverable_job(session, job_id, task_id=task_id)
    await session.commit()


async def _wait_for_cancellation(job_id: int, task_id: str | None) -> str:
    """Watch durable job state without sharing the executor's session."""
    await asyncio.sleep(_CANCEL_POLL_SECONDS)
    session_maker = get_celery_session_maker()
    # The claim just set heartbeat_at, so the first keep-alive is a full
    # interval away.
    last_keepalive = time.monotonic()
    while True:
        try:
            async with session_maker() as session:
                row = (
                    await session.execute(
                        select(
                            DeliverableJob.status,
                            DeliverableJob.celery_task_id,
                        ).where(DeliverableJob.id == job_id)
                    )
                ).one_or_none()
            if row is None or (task_id is not None and row.celery_task_id != task_id):
                return "superseded"
            if row.status in {
                DeliverableJobStatus.CANCELLING,
                DeliverableJobStatus.CANCELLED,
            }:
                return "cancelled"
            if row.status != DeliverableJobStatus.RUNNING:
                return "superseded"
            if time.monotonic() - last_keepalive >= _KEEPALIVE_SECONDS:
                # Set before the write: a failing database should be retried
                # at the keep-alive cadence, not every poll.
                last_keepalive = time.monotonic()
                async with session_maker() as session:
                    await touch_deliverable_job_heartbeat(
                        session, job_id, task_id=task_id
                    )
                    await session.commit()
        except asyncio.CancelledError:
            raise
        except Exception:
            logger.warning(
                "Could not poll cancellation state for deliverable job %s",
                job_id,
                exc_info=True,
            )
        await asyncio.sleep(_CANCEL_POLL_SECONDS)


async def _run_with_cancellation(
    work,
    *,
    job_id: int,
    task_id: str | None,
    sandbox_owner: str,
):
    work_task = asyncio.create_task(work)
    watcher = asyncio.create_task(_wait_for_cancellation(job_id, task_id))
    done, _ = await asyncio.wait(
        {work_task, watcher},
        return_when=asyncio.FIRST_COMPLETED,
    )
    if work_task in done:
        watcher.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await watcher
        return await work_task

    outcome = await watcher
    work_task.cancel()
    try:
        await (await get_registry()).terminate(sandbox_owner)
    except Exception:
        logger.warning(
            "Could not immediately terminate sandbox %s during cancellation",
            sandbox_owner,
            exc_info=True,
        )
    await asyncio.gather(work_task, return_exceptions=True)
    if outcome == "cancelled":
        raise DeliverableJobCancellationError
    raise _SupersededAttemptError


async def _execute_claimed_deliverable(
    session,
    job: DeliverableJob,
    *,
    job_id: int,
    task_id: str | None,
) -> dict[str, Any]:
    """Run the job on its Studio model (and fallback), or the chat model.

    A Studio job whose primary model fails is run once more on its fallback;
    a chat-started job has no Studio plan and runs as it always has.
    """
    plan = await plan_for_job(session, job_id)

    async def attempt(model) -> tuple[int, str]:
        if model is None:
            llm, agent_config = await _resolve_worker_model(session, job)
        else:
            llm, agent_config = await studio_llm_bundle(
                session, job.workspace_id, model
            )
        artifact_id = await _produce(
            session, job, llm, agent_config, job_id=job_id, task_id=task_id
        )
        return artifact_id, getattr(agent_config, "model_name", None) or "unknown"

    async def reset_for_fallback() -> None:
        await session.rollback()
        await session.refresh(job)

    produced_by = None
    if plan is None:
        artifact_id, _ = await attempt(None)
    else:
        artifact_id, produced_by = await run_with_fallback(
            plan, attempt, before_fallback=reset_for_fallback
        )
    ready = await complete_deliverable_job(
        session,
        job_id,
        artifact_id=artifact_id,
        task_id=task_id,
    )
    if ready is None:
        raise DeliverableJobCancellationError
    if produced_by is not None:
        await record_for_job(session, job_id, artifact_id, produced_by)
    await session.commit()
    return {"status": "ready", "job_id": job_id, "artifact_id": artifact_id}


async def _produce(
    session,
    job: DeliverableJob,
    llm,
    agent_config,
    *,
    job_id: int,
    task_id: str | None,
) -> int:
    """One run of the job on one model; returns the saved artifact's id."""
    if job.kind == ARTIFACT_KIND:
        return await _produce_artifact(
            session, job, llm, agent_config, job_id=job_id, task_id=task_id
        )
    owner_id, billing_tier, base_model = await _resolve_agent_billing_for_workspace(
        session,
        job.workspace_id,
        thread_id=job.thread_id,
    )
    billed_llm = _BillableQueuedLLM(
        llm,
        user_id=owner_id,
        workspace_id=job.workspace_id,
        billing_tier=billing_tier,
        base_model=base_model,
        quota_reserve_tokens=agent_config.quota_reserve_tokens,
        thread_id=job.thread_id,
        job_id=job_id,
    )
    return (await execute_video_deliverable(session, job, billed_llm)).artifact_id


async def _produce_artifact(
    session,
    job: DeliverableJob,
    llm,
    agent_config,
    *,
    job_id: int,
    task_id: str | None,
) -> int:
    """Run the deliverables subagent for one artifact job.

    Credit is not taken here: the Studio output (or chat tool) that queued the
    job held its price when it started, and the job's completion settles it
    (``app.billing.jobs``), so the run itself is not charged a second time.
    """
    started = ArtifactProgressTracker().current
    await _heartbeat_artifact(
        session, job_id, started.phase, started.progress, task_id=task_id
    )
    result = await execute_artifact_deliverable(
        session,
        job,
        llm,
        report_progress=JobProgressHeartbeat(
            get_celery_session_maker(),
            job_id,
            task_id=task_id,
            last_written=started,
        ),
    )
    return result.artifact_id


async def _heartbeat_artifact(
    session, job_id: int, phase: str, progress: int, *, task_id: str | None
) -> None:
    updated = await heartbeat_deliverable_job(
        session, job_id, phase=phase, progress=progress, task_id=task_id
    )
    if updated is None:
        await session.rollback()
        raise DeliverableJobCancellationError
    await session.commit()


async def _execute_queued_deliverable(
    job_id: int,
    *,
    task_id: str | None,
    retry_provider_failure: bool,
    retries: int = 0,
) -> dict[str, Any]:
    session_maker = get_celery_session_maker()
    async with session_maker() as session:
        job = await claim_deliverable_job(session, job_id, task_id=task_id)
        if job is None:
            await session.rollback()
            return {"status": "ignored", "job_id": job_id}
        await session.commit()
        sandbox_owner = _sandbox_owner(job)

        try:
            return await _run_with_cancellation(
                _execute_claimed_deliverable(
                    session,
                    job,
                    job_id=job_id,
                    task_id=task_id,
                ),
                job_id=job_id,
                task_id=task_id,
                sandbox_owner=sandbox_owner,
            )
        except DeliverableJobCancellationError:
            await session.rollback()
            await cancel_deliverable_job(session, job_id, task_id=task_id)
            await session.commit()
            return {"status": "cancelled", "job_id": job_id}
        except _SupersededAttemptError:
            await session.rollback()
            return {"status": "ignored", "job_id": job_id}
        except Exception as exc:
            await session.rollback()
            failure_code, transient = classify_deliverable_failure(exc, retries=retries)
            diagnostic = sanitize_internal_error(exc)
            logger.warning(
                "Queued deliverable job %s failed (%s, transient=%s)",
                job_id,
                failure_code.value,
                transient,
                exc_info=True,
            )
            if transient and retry_provider_failure:
                requeued = await requeue_claimed_deliverable_job(
                    session,
                    job_id,
                    task_id=task_id,
                    internal_error=diagnostic,
                )
                await session.commit()
                if requeued is not None:
                    return {"status": "retry", "job_id": job_id}
            await _finish_failure(
                session,
                job_id,
                failure_code=failure_code,
                diagnostic=diagnostic,
                task_id=task_id,
            )
            return {
                "status": "failed",
                "job_id": job_id,
                "failure_code": failure_code.value,
            }
        finally:
            try:
                await (await get_registry()).terminate(sandbox_owner)
            except Exception:
                logger.warning(
                    "Could not terminate sandbox for queued deliverable job %s",
                    job_id,
                    exc_info=True,
                )


@celery_app.task(
    bind=True,
    name=DELIVERABLE_JOB_TASK,
    acks_late=True,
    reject_on_worker_lost=True,
    max_retries=_MAX_PROVIDER_RETRIES,
    soft_time_limit=VIDEO_SPEC.soft_time_limit_seconds,
    time_limit=VIDEO_SPEC.hard_time_limit_seconds,
)
def execute_queued_deliverable(self, job_id: int) -> dict[str, Any]:
    bind_log_context(job_id=job_id)
    retries = int(getattr(self.request, "retries", 0) or 0)
    outcome = run_async_celery_task(
        lambda: _execute_queued_deliverable(
            job_id,
            task_id=getattr(self.request, "id", None),
            retry_provider_failure=retries < _MAX_PROVIDER_RETRIES,
            retries=retries,
        )
    )
    if outcome["status"] == "retry":
        raise self.retry(
            countdown=min(60, 2 ** (retries + 1)),
            exc=RuntimeError("transient deliverable provider failure"),
        )
    return outcome


async def _reconcile_stale_queued() -> int:
    dispatched = 0
    stale_sandbox_owners: list[str] = []
    session_maker = get_celery_session_maker()
    async with session_maker() as session:
        jobs = await list_stale_queued_jobs(
            session,
            updated_before=datetime.now(UTC) - _OUTBOX_STALE_AFTER,
            limit=_OUTBOX_BATCH_SIZE,
        )
        for job in jobs:
            try:
                get_deliverable_kind_spec(job.kind)
                if not job.celery_task_id:
                    raise ValueError("queued deliverable has no task identity")
                dispatch_deliverable_job(
                    job_id=job.id,
                    task_id=job.celery_task_id,
                )
                dispatched += 1
            except Exception:
                logger.warning(
                    "Could not reconcile queued deliverable job %s",
                    job.id,
                    exc_info=True,
                )
        cancelling = await list_stale_cancelling_jobs(
            session,
            heartbeat_before=datetime.now(UTC) - _CANCELLING_STALE_AFTER,
            limit=_OUTBOX_BATCH_SIZE,
        )
        for job in cancelling:
            cancelled = await cancel_deliverable_job(
                session,
                job.id,
                task_id=job.celery_task_id,
            )
            if cancelled is not None:
                stale_sandbox_owners.append(_sandbox_owner(job))
        orphans_to_dispatch = await _recover_orphaned_running(
            session, stale_sandbox_owners
        )
        await session.commit()
    for owner in stale_sandbox_owners:
        try:
            await (await get_registry()).terminate(owner)
        except Exception:
            logger.warning(
                "Could not terminate stale deliverable sandbox %s",
                owner,
                exc_info=True,
            )
    # Dispatched only after the dead attempt's sandbox is gone: the requeued
    # attempt keeps the same attempt number, so it would reuse its owner name.
    for job_id, task_id in orphans_to_dispatch:
        try:
            dispatch_deliverable_job(job_id=job_id, task_id=task_id)
            dispatched += 1
        except Exception:
            # Left queued; the outbox pass republishes it on a later run.
            logger.warning(
                "Could not dispatch recovered deliverable job %s",
                job_id,
                exc_info=True,
            )
    return dispatched


async def _recover_orphaned_running(
    session, stale_sandbox_owners: list[str]
) -> list[tuple[int, str]]:
    """Requeue running jobs whose worker died; fail ones that keep dying.

    Returns the (job id, task id) pairs to publish once the transaction has
    committed and their old sandboxes are terminated.
    """
    to_dispatch: list[tuple[int, str]] = []
    orphaned = await list_orphaned_running_jobs(
        session,
        heartbeat_before=datetime.now(UTC) - _RUNNING_STALE_AFTER,
        limit=_OUTBOX_BATCH_SIZE,
    )
    for job in orphaned:
        owner = _sandbox_owner(job)
        checkpoint = dict(job.checkpoint or {})
        requeues = int(checkpoint.get(_ORPHAN_REQUEUES_KEY, 0))
        if requeues >= _MAX_ORPHAN_REQUEUES:
            failed = await fail_deliverable_job(
                session,
                job.id,
                failure_code=DeliverableFailureCode.RENDER_FAILED,
                internal_error=(
                    f"worker stopped responding {requeues + 1} times; "
                    "not requeued again"
                ),
                task_id=job.celery_task_id,
            )
            if failed is not None:
                stale_sandbox_owners.append(owner)
                logger.warning(
                    "Deliverable job %s lost its worker %d times; failed",
                    job.id,
                    requeues + 1,
                )
            continue
        checkpoint[_ORPHAN_REQUEUES_KEY] = requeues + 1
        requeued = await requeue_claimed_deliverable_job(
            session,
            job.id,
            task_id=job.celery_task_id,
            internal_error="worker stopped responding; requeued",
            checkpoint=checkpoint,
        )
        if requeued is not None and job.celery_task_id:
            stale_sandbox_owners.append(owner)
            to_dispatch.append((job.id, job.celery_task_id))
            logger.warning(
                "Deliverable job %s lost its worker; requeued (%d of %d)",
                job.id,
                requeues + 1,
                _MAX_ORPHAN_REQUEUES,
            )
    return to_dispatch


@celery_app.task(name=RECONCILE_TASK)
def reconcile_stale_queued_deliverables() -> int:
    return run_async_celery_task(_reconcile_stale_queued)
