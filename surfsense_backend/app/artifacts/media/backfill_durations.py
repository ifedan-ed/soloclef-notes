"""Record the duration of videos and podcasts finished before it was stored.

Renders now record how long they play; older ones show no duration in the
Studio list. This reads each such file once from storage, measures it and
stores the result where a new render would have. Files that cannot be read
are skipped and reported, never altered. Safe to run again: anything that
already has a duration is left alone. Run it with
scripts/backfill_media_durations.py.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from app.artifacts.persistence import Artifact, ArtifactFile, ArtifactFileRole
from app.artifacts.storage import open_artifact_file_stream

from .duration import DURATION_KEY, probe_stream_duration

logger = logging.getLogger(__name__)


@dataclass
class BackfillReport:
    videos: list[int] = field(default_factory=list)
    podcasts: list[int] = field(default_factory=list)
    skipped: list[int] = field(default_factory=list)


def _primary(artifact: Artifact) -> ArtifactFile | None:
    return next(
        (file for file in artifact.files if file.role is ArtifactFileRole.PRIMARY),
        None,
    )


async def _measure(artifact: Artifact) -> float | None:
    primary = _primary(artifact)
    if primary is None:
        return None
    suffix = "." + (primary.original_filename.rsplit(".", 1)[-1] or "bin")
    try:
        return await probe_stream_duration(
            open_artifact_file_stream(primary), suffix=suffix
        )
    except Exception:
        logger.warning("Could not read artifact %s for its duration", artifact.id)
        return None


async def backfill_durations(session: AsyncSession) -> BackfillReport:
    from app.podcasts.persistence import Podcast, PodcastStatus

    report = BackfillReport()
    videos = (
        await session.scalars(
            select(Artifact)
            .options(selectinload(Artifact.files))
            .where(Artifact.format == "video")
            .order_by(Artifact.id)
        )
    ).all()
    for artifact in videos:
        if (artifact.artifact_metadata or {}).get(DURATION_KEY):
            continue
        seconds = await _measure(artifact)
        if seconds is None:
            report.skipped.append(artifact.id)
            continue
        # A new dict, so the JSONB column is seen as changed.
        artifact.artifact_metadata = {
            **(artifact.artifact_metadata or {}),
            DURATION_KEY: seconds,
        }
        report.videos.append(artifact.id)

    podcasts = (
        await session.scalars(
            select(Podcast)
            .where(
                Podcast.status == PodcastStatus.READY,
                Podcast.artifact_id.is_not(None),
                Podcast.duration_seconds.is_(None),
            )
            .order_by(Podcast.id)
        )
    ).all()
    for podcast in podcasts:
        artifact = await session.scalar(
            select(Artifact)
            .options(selectinload(Artifact.files))
            .where(
                Artifact.id == podcast.artifact_id,
                Artifact.workspace_id == podcast.workspace_id,
            )
        )
        seconds = await _measure(artifact) if artifact is not None else None
        if seconds is None:
            report.skipped.append(podcast.artifact_id)
            continue
        podcast.duration_seconds = round(seconds)
        report.podcasts.append(podcast.id)

    await session.commit()
    return report
