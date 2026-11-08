"""A prepared HTML fragment the run must carry into its saved page, verbatim.

The requester renders the fragment itself (a notebook report's embedded Studio
outputs) and the job places it in the sandbox for the model to include
unchanged. The model is asked, not trusted: after the run, a saved HTML page
that lacks the fragment's marker gets the fragment appended as one further
revision, so the page always ends up with it.
"""

from __future__ import annotations

import logging
from typing import Annotated

from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from app.artifacts.persistence import Artifact, ArtifactFileRole
from app.artifacts.service import ArtifactFileInput, save_artifact
from app.artifacts.storage import open_artifact_file_stream
from app.db import DeliverableJob
from app.sandbox import SandboxSession

logger = logging.getLogger(__name__)

MAX_FRAGMENT_CHARS = 600_000
_MAX_PAGE_BYTES = 15 * 1024 * 1024


class RequiredFragment(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    path: Annotated[str, Field(pattern=r"^/workspace/[a-z0-9][a-z0-9-]{0,60}\.html$")]
    html: Annotated[str, Field(min_length=1, max_length=MAX_FRAGMENT_CHARS)]
    marker: Annotated[str, Field(pattern=r"^[a-z][a-z0-9-]{2,63}$")]


async def place_fragment(sandbox: SandboxSession, fragment: RequiredFragment) -> None:
    await sandbox.write_file(fragment.path, fragment.html.encode("utf-8"))


async def ensure_fragment(
    session: AsyncSession,
    job: DeliverableJob,
    artifact_id: int,
    fragment: RequiredFragment,
) -> bool:
    """Append the fragment if the saved page lacks it; True when it was added."""
    artifact = await session.scalar(
        select(Artifact)
        .options(selectinload(Artifact.files), selectinload(Artifact.document))
        .where(Artifact.id == artifact_id, Artifact.workspace_id == job.workspace_id)
    )
    if artifact is None:
        return False
    primary = next(
        (file for file in artifact.files if file.role is ArtifactFileRole.PRIMARY),
        None,
    )
    if primary is None or primary.mime_type != "text/html":
        return False
    if primary.size_bytes > _MAX_PAGE_BYTES:
        return False
    data = bytearray()
    async for chunk in open_artifact_file_stream(primary):
        data.extend(chunk)
    page = bytes(data).decode("utf-8", errors="replace")
    if fragment.marker in page:
        return False

    logger.info(
        "Artifact %s was saved without its required fragment; appending it",
        artifact_id,
    )
    document = artifact.document
    await save_artifact(
        session,
        workspace_id=job.workspace_id,
        thread_id=job.thread_id,
        tool_call_id=job.tool_call_id,
        title=document.title or "Report",
        markdown_representation=document.source_markdown or document.content or "-",
        files=[
            ArtifactFileInput(
                data=f"{page.rstrip()}\n{fragment.html}\n".encode(),
                filename=primary.original_filename,
                mime_type="text/html",
            )
        ],
        artifact_id=artifact.id,
        expected_generation=artifact.generation,
        format=artifact.format,
    )
    await session.commit()
    return True
