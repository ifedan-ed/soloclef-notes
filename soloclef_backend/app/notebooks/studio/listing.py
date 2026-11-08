"""The studio list: each generation with its live lifecycle state.

State is read from where the generation actually runs (the podcast row or the
deliverable job) at request time, so the list never drifts from the truth.
"""

from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.artifacts.media.duration import DURATION_KEY
from app.artifacts.persistence import Artifact
from app.db import DeliverableJob, DeliverableJobStatus
from app.model_display_names import friendly_name
from app.podcasts.persistence import Podcast, PodcastStatus

from ..persistence import Notebook, NotebookOutput


def shown_generated_by(info: dict | None) -> dict | None:
    """``generated_by`` with its model ids as the names people see."""
    if not info:
        return info
    shown = dict(info)
    for key in ("model", "primary"):
        if shown.get(key):
            shown[key] = friendly_name(shown[key])
    return shown


StudioStatus = Literal["queued", "running", "ready", "failed", "cancelled"]

_PODCAST_STATUS: dict[PodcastStatus, tuple[StudioStatus, str | None]] = {
    PodcastStatus.PENDING: ("queued", None),
    PodcastStatus.AWAITING_BRIEF: ("queued", None),
    PodcastStatus.DRAFTING: ("running", "drafting"),
    PodcastStatus.AWAITING_REVIEW: ("running", "drafting"),
    PodcastStatus.RENDERING: ("running", "rendering"),
    PodcastStatus.READY: ("ready", None),
    PodcastStatus.FAILED: ("failed", None),
    PodcastStatus.CANCELLED: ("cancelled", None),
}

_JOB_STATUS: dict[DeliverableJobStatus, StudioStatus] = {
    DeliverableJobStatus.QUEUED: "queued",
    DeliverableJobStatus.RUNNING: "running",
    DeliverableJobStatus.CANCELLING: "running",
    DeliverableJobStatus.CANCELLED: "cancelled",
    DeliverableJobStatus.FAILED: "failed",
    DeliverableJobStatus.READY: "ready",
}


class StudioOutputRead(BaseModel):
    id: int
    kind: str
    title: str
    status: StudioStatus
    phase: str | None = None
    progress: int | None = None
    failure_code: str | None = None
    artifact_id: int | None = None
    artifact_format: str | None = None
    duration_seconds: int | None = None
    source_count: int
    options: dict
    # {"model": ..., "fallback": true?} once a Studio run has finished drafting.
    generated_by: dict | None = None
    created_at: datetime


def _stored_duration(metadata) -> int | None:
    """Whole seconds from an artifact's metadata, if a render recorded them."""
    if not isinstance(metadata, dict):
        return None
    value = metadata.get(DURATION_KEY)
    if isinstance(value, bool) or not isinstance(value, int | float) or value <= 0:
        return None
    return round(value)


def _status_enum(enum_cls, value):
    return value if isinstance(value, enum_cls) else enum_cls(value)


async def list_outputs(
    session: AsyncSession, notebook: Notebook, *, output_id: int | None = None
) -> list[StudioOutputRead]:
    query = (
        select(NotebookOutput, Podcast, DeliverableJob)
        .outerjoin(Podcast, Podcast.id == NotebookOutput.podcast_id)
        .outerjoin(
            DeliverableJob, DeliverableJob.id == NotebookOutput.deliverable_job_id
        )
        .where(
            NotebookOutput.notebook_id == notebook.id,
            NotebookOutput.workspace_id == notebook.workspace_id,
        )
        .order_by(NotebookOutput.created_at.desc(), NotebookOutput.id.desc())
    )
    if output_id is not None:
        query = query.where(NotebookOutput.id == output_id)
    rows = (await session.execute(query)).all()

    artifact_ids = {
        artifact_id
        for _, podcast, job in rows
        for artifact_id in (
            podcast.artifact_id if podcast else None,
            job.artifact_id if job else None,
        )
        if artifact_id
    }
    formats: dict[int, str] = {}
    durations: dict[int, int] = {}
    if artifact_ids:
        for artifact_id, artifact_format, metadata in (
            await session.execute(
                select(Artifact.id, Artifact.format, Artifact.artifact_metadata).where(
                    Artifact.id.in_(artifact_ids),
                    Artifact.workspace_id == notebook.workspace_id,
                )
            )
        ).all():
            formats[artifact_id] = artifact_format
            seconds = _stored_duration(metadata)
            if seconds is not None:
                durations[artifact_id] = seconds

    items: list[StudioOutputRead] = []
    for output, podcast, job in rows:
        status: StudioStatus = "failed"
        phase = progress = failure = artifact_id = duration = None
        if podcast is not None:
            status, phase = _PODCAST_STATUS[_status_enum(PodcastStatus, podcast.status)]
            artifact_id = podcast.artifact_id
            duration = podcast.duration_seconds
        elif job is not None:
            status = _JOB_STATUS[_status_enum(DeliverableJobStatus, job.status)]
            phase, progress, failure = job.phase, job.progress, job.failure_code
            artifact_id = job.artifact_id
        if duration is None and artifact_id:
            duration = durations.get(artifact_id)
        items.append(
            StudioOutputRead(
                id=output.id,
                kind=output.kind,
                title=output.title,
                status=status,
                phase=phase,
                progress=progress,
                failure_code=failure,
                artifact_id=artifact_id,
                artifact_format=formats.get(artifact_id) if artifact_id else None,
                duration_seconds=duration,
                source_count=len(output.source_document_ids or []),
                options=output.options or {},
                generated_by=shown_generated_by(output.generated_by),
                created_at=output.created_at,
            )
        )
    return items
