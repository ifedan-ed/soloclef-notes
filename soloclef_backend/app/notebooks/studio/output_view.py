"""What a Studio output's viewer shows around it: its prompt and sources.

NotebookLM's output view carries a "View prompt and N sources" chip under the
viewer. The prompt is the focus the output was asked with (kept in its
options); the sources are the ones it was made from.

The "Good content / Bad content" rating was removed on 2026-09-27. Ratings
given before then stay in the artifact metadata (``output_view`` and the
report view's entry) untouched; nothing reads or writes them any more.
"""

from __future__ import annotations

from typing import Any

from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.artifacts.persistence import Artifact

from ..persistence import Notebook, NotebookOutput
from .listing import list_outputs
from .report_view.service import SourceRef, _sources


class OutputNotFoundError(LookupError):
    """No ready output with an artifact under that id in this notebook."""


class OutputViewRead(BaseModel):
    output_id: int
    artifact_id: int
    kind: str
    prompt: str
    sources: list[SourceRef]


def output_prompt(options: Any) -> str:
    """The focus the output was asked with ("" when none was given)."""
    if not isinstance(options, dict):
        return ""
    value = options.get("focus")
    return value.strip() if isinstance(value, str) else ""


async def _load(
    session: AsyncSession, notebook: Notebook, output_id: int
) -> tuple[NotebookOutput, Artifact]:
    output = await session.scalar(
        select(NotebookOutput).where(
            NotebookOutput.id == output_id,
            NotebookOutput.notebook_id == notebook.id,
            NotebookOutput.workspace_id == notebook.workspace_id,
        )
    )
    if output is None:
        raise OutputNotFoundError
    listed = await list_outputs(session, notebook, output_id=output.id)
    if not listed or listed[0].status != "ready" or not listed[0].artifact_id:
        raise OutputNotFoundError
    query = select(Artifact).where(
        Artifact.id == listed[0].artifact_id,
        Artifact.workspace_id == notebook.workspace_id,
    )
    artifact = await session.scalar(query)
    if artifact is None:
        raise OutputNotFoundError
    return output, artifact


async def _read(
    session: AsyncSession,
    notebook: Notebook,
    output: NotebookOutput,
    artifact: Artifact,
) -> OutputViewRead:
    return OutputViewRead(
        output_id=output.id,
        artifact_id=artifact.id,
        kind=output.kind,
        prompt=output_prompt(output.options),
        sources=await _sources(session, notebook, output.source_document_ids),
    )


async def output_view(
    session: AsyncSession, notebook: Notebook, output_id: int
) -> OutputViewRead:
    output, artifact = await _load(session, notebook, output_id)
    return await _read(session, notebook, output, artifact)
