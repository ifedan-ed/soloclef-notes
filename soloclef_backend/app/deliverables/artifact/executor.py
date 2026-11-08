"""Run one queued artifact deliverable through the deliverables subagent.

The same engine a chat's ``task(deliverables)`` and the artifact-generation API
drive: sandbox skill -> verify_artifact -> save_artifact, bound to the job's own
thread so the sandbox key and the artifact's attribution are the job's. No
chat is streamed to and nothing is checkpointed beyond this one run.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass

from langgraph.checkpoint.memory import InMemorySaver
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import DeliverableJob
from app.deliverables.jobs.policy import ARTIFACT_KIND

from .output_formats import pick_requested_artifact
from .progress import (
    SAVING,
    ArtifactProgressObserver,
    ArtifactProgressTracker,
    ProgressReporter,
)
from .request import ArtifactJobRequestV1
from .required_fragment import ensure_fragment, place_fragment

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class ArtifactExecutionResult:
    artifact_id: int


class NoArtifactProducedError(RuntimeError):
    """The subagent finished without a verified, saved artifact."""

    def __init__(self, message: str) -> None:
        super().__init__(f"no verified artifact was saved: {message[:300]}")


def _infographic_layout_kwargs(request: ArtifactJobRequestV1) -> dict:
    """The Studio dialog's infographic canvas and detail, when it chose any."""
    if (
        request.infographic_orientation is None
        and request.infographic_detail is None
        and not request.infographic_description
    ):
        return {}
    from app.artifacts.infographic.layout import InfographicLayout

    return {
        "infographic_layout": InfographicLayout(
            orientation=request.infographic_orientation,
            detail=request.infographic_detail,
            description=request.infographic_description,
        )
    }


async def _ignore_progress(_update) -> None:
    return None


def artifact_sandbox_owner(job: DeliverableJob) -> str:
    """The subagent keys its sandbox by the thread it runs on."""
    return str(job.thread_id)


async def execute_artifact_deliverable(
    session: AsyncSession,
    job: DeliverableJob,
    llm,
    *,
    run_subagent=None,
    get_sandbox=None,
    report_progress: ProgressReporter | None = None,
) -> ArtifactExecutionResult:
    """Run the job's prompt; ``report_progress`` receives the run's live phases."""
    request = ArtifactJobRequestV1.model_validate(job.request)
    if job.kind != ARTIFACT_KIND:
        raise ValueError("artifact executor only accepts artifact deliverable jobs")
    if job.thread_id is None or request.root_thread_id != job.thread_id:
        raise ValueError("queued artifact request must name its root thread")

    if run_subagent is None:
        from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.run import (
            run_deliverable_subagent as run_subagent,
        )

    tracker = ArtifactProgressTracker()
    observer = ArtifactProgressObserver(tracker, report_progress or _ignore_progress)
    progress_kwargs = {} if report_progress is None else {"callbacks": [observer]}

    fragment = request.required_fragment
    if fragment is not None:
        if get_sandbox is None:
            from app.sandbox import get_registry

            async def get_sandbox():
                return await (await get_registry()).get_session(
                    job.thread_id, job.workspace_id
                )

        # The subagent keys its sandbox by this same thread, so it finds the file.
        await place_fragment(await get_sandbox(), fragment)
        await observer.emit(tracker.nudge())

    result = await run_subagent(
        session=session,
        workspace_id=job.workspace_id,
        thread_id=job.thread_id,
        prompt=request.prompt,
        llm=llm,
        checkpointer=InMemorySaver(),
        created_by_id=str(job.created_by_id) if job.created_by_id else None,
        infographic_style=request.infographic_style,
        item_count=request.item_count,
        **_infographic_layout_kwargs(request),
        **progress_kwargs,
    )
    if not result.artifacts:
        raise NoArtifactProducedError(result.message or "the run returned no artifact")
    chosen = pick_requested_artifact(request.output, result.artifacts)
    if chosen is None:
        saved = ", ".join(sorted({a.format for a in result.artifacts}))
        raise NoArtifactProducedError(
            f"the run saved {saved or 'unknown'} but the request was {request.output}"
        )
    artifact_id = chosen.artifact_id
    await observer.emit(tracker.advance_to(SAVING))
    if fragment is not None:
        try:
            await ensure_fragment(session, job, artifact_id, fragment)
            await observer.emit(tracker.nudge())
        except Exception:
            # The report itself is saved and verified; missing embeds are not
            # worth failing it over.
            logger.exception("Could not add the required fragment to %s", artifact_id)
            await session.rollback()
    return ArtifactExecutionResult(artifact_id=artifact_id)
