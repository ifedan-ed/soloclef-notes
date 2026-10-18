"""Every file of a shared Studio output, so the public page can open it in the
same viewer its owner sees.

A kind's viewer needs more than the one main file: a slide deck shows its PDF
preview, flashcards and a quiz read their JSON, a data table its workbook. The
public page is sent the shared artifact's files in the in-app manifest's shape
(``file_id``, ``role``, ``filename``, ``mime_type``, ``size_bytes``,
``content_url``), each served by ``/public/{token}/artifacts/{id}/files/{file_id}/content``.

Files are read when the link is opened, as the main file always was, so a link
made before this existed shows its output whole without being made again. The
snapshot's ``artifact_ids`` stays the allowlist: a token reaches only the files
of the artifact it shared. Storage keys and backends never leave the server.
"""

from __future__ import annotations

from urllib.parse import quote

from fastapi import Request, Response
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.artifacts.persistence import Artifact, ArtifactFile, ArtifactFileRole

API = "/api/v1/public"

# Only these a browser may show from the app's origin when opened directly;
# anything else is sandboxed (as the live notebook link's files are).
_INLINE_SAFE = {"application/pdf", "video/mp4"}


def shared_file_url(token: str, artifact_id: int, file_id: int) -> str:
    return (
        f"{API}/{quote(token, safe='')}/artifacts/{artifact_id}/files/{file_id}/content"
    )


def _ordered(files: list[ArtifactFile]) -> list[ArtifactFile]:
    """The main file first, as the owner's manifest lists them."""
    return sorted(
        files, key=lambda item: (item.role is not ArtifactFileRole.PRIMARY, item.id)
    )


async def shared_output_files(
    session: AsyncSession, token: str, artifact_id: int
) -> tuple[int, list[dict]]:
    """The shared artifact's generation and its files, with this link's URLs."""
    artifact = await session.scalar(select(Artifact).where(Artifact.id == artifact_id))
    if artifact is None:
        return 1, []
    files = (
        await session.scalars(
            select(ArtifactFile).where(ArtifactFile.artifact_id == artifact_id)
        )
    ).all()
    return artifact.generation or 1, [
        {
            "file_id": file.id,
            "role": file.role.value,
            "filename": file.original_filename,
            "mime_type": file.mime_type or "application/octet-stream",
            "size_bytes": file.size_bytes,
            "content_url": shared_file_url(token, artifact_id, file.id),
        }
        for file in _ordered(list(files))
    ]


async def snapshot_artifact_file_by_id(
    session: AsyncSession,
    allowed_artifact_ids: list[int],
    artifact_id: int,
    file_id: int,
) -> ArtifactFile | None:
    """One file of an artifact the snapshot references, or None."""
    if artifact_id not in allowed_artifact_ids:
        return None
    return await session.scalar(
        select(ArtifactFile).where(
            ArtifactFile.id == file_id,
            ArtifactFile.artifact_id == artifact_id,
        )
    )


def shared_file_response(record: ArtifactFile, request: Request) -> Response:
    """Ranges and ETags as in the app; revalidated on every use, so a revoked
    or expired link stops serving at once; anything but a PDF or MP4 is a
    sandboxed download when opened directly (the viewers read it with fetch)."""
    from app.routes.artifacts_routes import artifact_file_response

    response = artifact_file_response(record, request)
    response.headers["Cache-Control"] = "private, no-cache"
    mime = (record.mime_type or "").split(";")[0].strip().lower()
    if mime not in _INLINE_SAFE:
        response.headers["Content-Security-Policy"] = "sandbox"
    return response
