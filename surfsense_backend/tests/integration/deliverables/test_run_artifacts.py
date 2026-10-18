"""A run's saved artifacts come back in the order it saved them, with their format."""

from __future__ import annotations

import uuid

import pytest
from sqlalchemy.ext.asyncio import AsyncSession

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.run import (
    _hydrate_artifacts,
)
from app.artifacts.persistence import Artifact
from app.db import Document, DocumentType

pytestmark = pytest.mark.integration


async def _artifact(session: AsyncSession, workspace_id: int, fmt: str) -> int:
    document = Document(
        title=f"{fmt} output",
        document_type=DocumentType.ARTIFACT,
        path=f"/artifacts/{uuid.uuid4().hex}.md",
        content="# x",
        source_markdown="# x",
        content_hash=uuid.uuid4().hex,
        workspace_id=workspace_id,
        status={"state": "ready"},
    )
    session.add(document)
    await session.flush()
    artifact = Artifact(document_id=document.id, workspace_id=workspace_id, format=fmt)
    session.add(artifact)
    await session.flush()
    return artifact.id


async def test_hydration_keeps_the_run_order_and_reports_formats(
    db_session: AsyncSession, db_workspace
) -> None:
    older = await _artifact(db_session, db_workspace.id, "png")
    newer = await _artifact(db_session, db_workspace.id, "pptx")

    hydrated = await _hydrate_artifacts(db_session, db_workspace.id, [newer, older])

    assert [(a.artifact_id, a.format) for a in hydrated] == [
        (newer, "pptx"),
        (older, "png"),
    ]
