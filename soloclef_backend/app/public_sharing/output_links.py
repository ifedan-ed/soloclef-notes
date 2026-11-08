"""A public link to one Studio output: a frozen copy of its text and its files.

The snapshot keeps the output's title, kind, format and Markdown as they were
when it was shared, plus the artifact id the public file routes may serve
(``output_files.py`` lists that artifact's files when the link is opened, so
the public page opens it in the owner's viewer for its kind). Nothing else
about the notebook, its sources or its members goes out.

Sharing an output again while its content is unchanged returns the same link
with the title it has now (a rename shows on the link) and the expiry just
chosen; a changed output gets a new link, and the old one keeps its copy.
"""

from __future__ import annotations

import hashlib
import secrets
from datetime import UTC, datetime

from fastapi import HTTPException
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from app.artifacts.persistence import Artifact, ArtifactFile, ArtifactFileRole
from app.auth.context import AuthContext
from app.config import config
from app.db import Permission, PublicChatSnapshot
from app.notebooks.persistence import Notebook, NotebookOutput
from app.notebooks.studio.listing import list_outputs
from app.utils.rbac import check_permission

from .expiry import expires_at_for, is_expired
from .inline_media import display_for


async def _ready_artifact(
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
        raise HTTPException(status_code=404, detail="Output not found")
    listed = await list_outputs(session, notebook, output_id=output.id)
    if not listed or listed[0].status != "ready" or not listed[0].artifact_id:
        raise HTTPException(
            status_code=400, detail="Only a finished output can be shared"
        )
    artifact = await session.scalar(
        select(Artifact)
        .options(selectinload(Artifact.document))
        .where(
            Artifact.id == listed[0].artifact_id,
            Artifact.workspace_id == notebook.workspace_id,
        )
    )
    if artifact is None:
        raise HTTPException(status_code=404, detail="Output not found")
    return output, artifact


async def _primary_file(session: AsyncSession, artifact_id: int) -> dict | None:
    file = await session.scalar(
        select(ArtifactFile).where(
            ArtifactFile.artifact_id == artifact_id,
            ArtifactFile.role == ArtifactFileRole.PRIMARY,
        )
    )
    if file is None:
        return None
    return {
        "filename": file.original_filename,
        "mime_type": file.mime_type,
        "size_bytes": file.size_bytes,
        "display": display_for(file.mime_type),
    }


def _retitled(snapshot_data: dict | None, title: str) -> dict:
    data = dict(snapshot_data or {})
    data["title"] = title
    if isinstance(data.get("output"), dict):
        data["output"] = {**data["output"], "title": title}
    return data


async def create_output_snapshot(
    session: AsyncSession,
    auth: AuthContext,
    notebook: Notebook,
    output_id: int,
    expires_in_days: int | None,
) -> dict:
    await check_permission(
        session,
        auth,
        notebook.workspace_id,
        Permission.PUBLIC_SHARING_CREATE.value,
        "You don't have permission to create public share links",
    )
    output, artifact = await _ready_artifact(session, notebook, output_id)
    document = artifact.document
    markdown = (document.source_markdown or document.content or "") if document else ""
    output_data = {
        "title": output.title,
        "kind": output.kind,
        "format": artifact.format,
        "markdown": markdown,
        "artifact_id": artifact.id,
        "file": await _primary_file(session, artifact.id),
    }
    content_hash = hashlib.sha256(
        f"{artifact.id}:{artifact.generation}:{markdown}".encode()
    ).hexdigest()
    expires_at = expires_at_for(expires_in_days)
    frontend_url = (config.NEXT_FRONTEND_URL or "").rstrip("/")

    existing = await session.scalar(
        select(PublicChatSnapshot).where(
            PublicChatSnapshot.notebook_output_id == output.id,
            PublicChatSnapshot.content_hash == content_hash,
        )
    )
    if existing is not None and is_expired(existing.expires_at):
        await session.delete(existing)
        await session.flush()
        existing = None
    if existing is not None:
        existing.expires_at = expires_at
        if (existing.snapshot_data or {}).get("title") != output.title:
            # A new dict, so the JSONB column is written.
            existing.snapshot_data = _retitled(existing.snapshot_data, output.title)
        await session.commit()
        return {
            "snapshot_id": existing.id,
            "share_token": existing.share_token,
            "public_url": f"{frontend_url}/public/{existing.share_token}",
            "is_new": False,
            "expires_at": existing.expires_at,
        }

    snapshot = PublicChatSnapshot(
        thread_id=None,
        notebook_output_id=output.id,
        workspace_id=notebook.workspace_id,
        share_token=secrets.token_urlsafe(48),
        content_hash=content_hash,
        snapshot_data={
            "kind": "output",
            "title": output.title,
            "snapshot_at": datetime.now(UTC).isoformat(),
            "output": output_data,
            "artifact_ids": [artifact.id],
        },
        message_ids=[],
        created_by_user_id=auth.user.id,
        expires_at=expires_at,
    )
    session.add(snapshot)
    await session.commit()
    await session.refresh(snapshot)
    return {
        "snapshot_id": snapshot.id,
        "share_token": snapshot.share_token,
        "public_url": f"{frontend_url}/public/{snapshot.share_token}",
        "is_new": True,
        "expires_at": snapshot.expires_at,
    }
