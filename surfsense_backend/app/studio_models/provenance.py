"""Record which model produced a Studio output.

Kept on the notebook output (the Studio list reads it) and, for deliverable
jobs, on the artifact's metadata too, so the file itself says where it came
from even outside the notebook.
"""

from __future__ import annotations

from typing import Any

from sqlalchemy import func, literal, update
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.ext.asyncio import AsyncSession

from app.artifacts.persistence import Artifact
from app.notebooks.persistence import NotebookOutput

GENERATED_BY_KEY = "generated_by"


async def record_for_job(
    session: AsyncSession, job_id: int, artifact_id: int, info: dict[str, Any]
) -> None:
    await session.execute(
        update(NotebookOutput)
        .where(NotebookOutput.deliverable_job_id == job_id)
        .values(generated_by=info)
    )
    await session.execute(
        update(Artifact)
        .where(Artifact.id == artifact_id)
        .values(
            artifact_metadata=func.coalesce(
                Artifact.artifact_metadata, literal({}, JSONB)
            ).op("||")(literal({GENERATED_BY_KEY: info}, JSONB))
        )
    )


async def record_for_podcast(
    session: AsyncSession, podcast_id: int, info: dict[str, Any]
) -> None:
    await session.execute(
        update(NotebookOutput)
        .where(NotebookOutput.podcast_id == podcast_id)
        .values(generated_by=info)
    )
