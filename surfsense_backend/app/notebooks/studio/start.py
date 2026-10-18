"""Start a studio generation from a notebook, without a chat turn.

Audio goes through the podcast lifecycle (brief proposed and approved at once,
since the dialog already chose the options). Everything else becomes a durable
deliverable job — the queued video path, or the ``artifact`` kind that runs the
deliverables subagent headlessly. Each generation gets its own hidden thread,
because the sandbox and the artifact's attribution are keyed by thread and two
generations must not share (or tear down) one sandbox.
"""

from __future__ import annotations

import logging
import uuid
from collections.abc import Callable
from dataclasses import dataclass
from uuid import UUID

from pydantic import ValidationError
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import ARTIFACT_API_SOURCE, ChatVisibility, DeliverableJob, NewChatThread
from app.deliverables.artifact.request import ArtifactJobRequestV1
from app.deliverables.artifact.required_fragment import RequiredFragment
from app.deliverables.jobs.dispatch import dispatch_deliverable_job
from app.deliverables.jobs.policy import ARTIFACT_KIND, VIDEO_KIND
from app.deliverables.jobs.service import create_deliverable_job
from app.podcasts.service import PodcastService
from app.workspace_instructions.load import load_workspace_instructions

from ..persistence import Notebook, NotebookOutput
from . import briefs
from .audio_brief import propose_audio_spec
from .options import AudioRequest, ReportRequest, VideoRequest
from .report_embeds.collect import collect_embeds
from .report_embeds.labels import labels_for
from .report_embeds.render import EMBED_MARKER, render_embeds
from .source_material import (
    SourceDocument,
    compile_material,
    whole_source_material,
)

logger = logging.getLogger(__name__)

EMBEDS_PATH = "/workspace/studio-embeds.html"


@dataclass
class Dispatchers:
    """Seams to the task queue, replaced in tests."""

    deliverable: Callable[..., None] = dispatch_deliverable_job
    podcast_draft: Callable[[int, int], None] | None = None

    def draft_podcast(self, podcast_id: int, workspace_id: int) -> None:
        if self.podcast_draft is not None:
            self.podcast_draft(podcast_id, workspace_id)
            return
        from app.podcasts.tasks import draft_transcript_task

        draft_transcript_task.delay(podcast_id, workspace_id)


def tool_call_id_for(notebook_id: int, request_id: str | None) -> str:
    """Namespaced so it can never collide with a chat tool call's id."""
    return f"notebook-studio:{notebook_id}:{request_id or uuid.uuid4().hex}"


async def _studio_thread(
    session: AsyncSession, notebook: Notebook, user_id: UUID, title: str
) -> NewChatThread:
    thread = NewChatThread(
        title=title[:120] or "Studio",
        workspace_id=notebook.workspace_id,
        created_by_id=user_id,
        visibility=ChatVisibility.PRIVATE,
        source=ARTIFACT_API_SOURCE,
        notebook_id=notebook.id,
    )
    session.add(thread)
    await session.flush()
    return thread


async def _output_for_tool_call(
    session: AsyncSession, notebook: Notebook, tool_call_id: str
) -> NotebookOutput | None:
    return await session.scalar(
        select(NotebookOutput)
        .join(DeliverableJob, DeliverableJob.id == NotebookOutput.deliverable_job_id)
        .where(
            NotebookOutput.notebook_id == notebook.id,
            DeliverableJob.workspace_id == notebook.workspace_id,
            DeliverableJob.tool_call_id == tool_call_id,
        )
    )


async def _report_embeds(
    session: AsyncSession, notebook: Notebook, request
) -> tuple[RequiredFragment | None, list[str]]:
    """An interactive report's section embedding the notebook's other outputs."""
    if not (isinstance(request, ReportRequest) and request.format == "interactive"):
        return None, []
    embeds = await collect_embeds(session, notebook)
    html = render_embeds(embeds, labels_for(request.language))
    if html is None:
        return None, []
    try:
        fragment = RequiredFragment(path=EMBEDS_PATH, html=html, marker=EMBED_MARKER)
    except ValidationError:
        logger.warning("Studio embeds for notebook %s are too large", notebook.id)
        return None, []
    return fragment, embeds.kinds


async def start_generation(
    session: AsyncSession,
    *,
    notebook: Notebook,
    request,
    title: str,
    documents: list[SourceDocument],
    user_id: UUID,
    dispatchers: Dispatchers | None = None,
    charge=None,
    max_video_seconds: int | None = None,
) -> NotebookOutput:
    """``charge`` is the credit hold taken for this output (app.billing); it is
    named after the job before the job is published, so the job's end settles
    or releases it. ``max_video_seconds`` is the requester's plan cap."""
    from app.billing import jobs as billing_jobs, meter

    dispatchers = dispatchers or Dispatchers()
    billing_sessions = meter.sibling_sessions(session)
    output = NotebookOutput(
        notebook_id=notebook.id,
        workspace_id=notebook.workspace_id,
        kind=request.kind,
        title=title,
        options=request.model_dump(
            mode="json", exclude={"source_ids", "title", "request_id"}
        ),
        source_document_ids=[document.id for document in documents],
        created_by_id=user_id,
    )

    if isinstance(request, AudioRequest):
        plan = briefs.audio_plan(request)
        service = PodcastService(session)
        podcast = await service.create(
            title=title, workspace_id=notebook.workspace_id, thread_id=None
        )
        # The whole source; the transcript fits it to its model (see
        # app/podcasts/generation/transcript/source_fit.py).
        podcast.source_content = whole_source_material(documents).text
        spec = await propose_audio_spec(
            session,
            workspace_id=notebook.workspace_id,
            plan=plan,
            language=request.language,
        )
        await service.attach_brief(podcast, spec)
        await service.begin_drafting(podcast)
        output.podcast_id = podcast.id
        session.add(output)
        await session.commit()
        await meter.attach_reference(charge, billing_jobs.podcast_reference(podcast.id))
        dispatchers.draft_podcast(podcast.id, notebook.workspace_id)
        return output

    tool_call_id = tool_call_id_for(notebook.id, request.request_id)
    repeated = await _output_for_tool_call(session, notebook, tool_call_id)
    if repeated is not None:
        # A repeated click: the first one holds the credit.
        await meter.release(
            charge, reason="repeated request", session_factory=billing_sessions
        )
        return repeated

    thread = await _studio_thread(session, notebook, user_id, title)
    output.thread_id = thread.id
    workspace_instructions = await load_workspace_instructions(
        session, notebook.workspace_id
    )
    if isinstance(request, VideoRequest):
        kind = VIDEO_KIND
        source = whole_source_material(documents)
        job_request = {
            "version": 1,
            "brief": " ".join(
                briefs.video_brief(
                    request, workspace_instructions=workspace_instructions
                ).split()
            ),
            "source_material": source.text or None,
            "source_condensed": source.condensed,
            "source_references": [document.title[:1000] for document in documents][:25],
            "revision_artifact_id": None,
            "root_thread_id": thread.id,
        }
        if max_video_seconds:
            job_request["max_seconds"] = int(max_video_seconds)
        job_request["source_references"] = list(
            dict.fromkeys(
                " ".join(ref.split()) for ref in job_request["source_references"]
            )
        )
    else:
        kind = ARTIFACT_KIND
        fragment, embedded = await _report_embeds(session, notebook, request)
        job_request = ArtifactJobRequestV1(
            version=1,
            output=briefs.artifact_output(request),
            prompt=briefs.artifact_prompt(
                request,
                title=title,
                material=compile_material(documents, briefs.ARTIFACT_MATERIAL_CHARS),
                embeds=(fragment.path, embedded) if fragment else None,
                workspace_instructions=workspace_instructions,
            ),
            root_thread_id=thread.id,
            infographic_style=briefs.infographic_style(request),
            **briefs.infographic_job_fields(request),
            required_fragment=fragment,
            item_count=briefs.item_count(request),
        ).model_dump(mode="json")

    job, created = await create_deliverable_job(
        session,
        kind=kind,
        title=title,
        workspace_id=notebook.workspace_id,
        thread_id=thread.id,
        created_by_id=user_id,
        tool_call_id=tool_call_id,
        request=job_request,
    )
    if not created:
        # Lost a race with the same click; the winner owns the output row.
        await session.rollback()
        await session.refresh(notebook)
        existing = await _output_for_tool_call(session, notebook, tool_call_id)
        await meter.release(
            charge, reason="repeated request", session_factory=billing_sessions
        )
        if existing is not None:
            return existing
        raise ValueError("this generation is already starting")
    output.deliverable_job_id = job.id
    session.add(output)
    task_id = job.celery_task_id
    job_id = job.id
    await session.commit()
    await meter.attach_reference(
        charge,
        billing_jobs.deliverable_reference(job_id),
        session_factory=billing_sessions,
    )
    if created and task_id:
        try:
            dispatchers.deliverable(job_id=job_id, task_id=task_id)
        except Exception:
            # The committed queued row is the outbox; reconciliation republishes it.
            logger.exception("Could not publish studio deliverable job %s", job_id)
    return output
