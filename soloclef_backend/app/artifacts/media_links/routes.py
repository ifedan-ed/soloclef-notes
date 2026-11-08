"""Issue media links for the signed-in viewer, and serve media through them."""

from __future__ import annotations

import uuid
from datetime import UTC, datetime

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from pydantic import BaseModel, Field
from sqlalchemy import or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.artifacts.persistence import Artifact, ArtifactFile
from app.auth.context import AuthContext
from app.config import config as app_config
from app.db import Permission, User, get_async_session
from app.users import require_session_context
from app.utils.rbac import check_permission

from .token import issue_media_token, new_claim, read_media_token

router = APIRouter(tags=["artifacts"])

MAX_LINKS_PER_REQUEST = 24
PUBLIC_MEDIA_PATH = "/api/v1/public/artifact-media"


def _is_media(mime_type: str | None) -> bool:
    return bool(mime_type) and mime_type.split("/", 1)[0] in {"audio", "video"}


class MediaFileRef(BaseModel):
    artifact_id: int = Field(gt=0)
    file_id: int = Field(gt=0)


class MediaLinkRequest(BaseModel):
    files: list[MediaFileRef] = Field(min_length=1, max_length=MAX_LINKS_PER_REQUEST)


class MediaLinkRead(BaseModel):
    artifact_id: int
    file_id: int
    url: str
    mime_type: str
    expires_at: datetime


class MediaLinksRead(BaseModel):
    links: list[MediaLinkRead]


@router.post(
    "/workspaces/{workspace_id}/artifacts/media-links",
    response_model=MediaLinksRead,
)
async def issue_media_links(
    workspace_id: int,
    body: MediaLinkRequest,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(require_session_context),
) -> MediaLinksRead:
    """Links for the audio and video files named, in this workspace only.

    Anything else — another workspace's file, a document, a file that no
    longer exists — is left out rather than refused, so a report whose
    embedded output was deleted still loads with that one player missing.
    """
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.ARTIFACTS_READ.value,
        "You don't have permission to read artifacts in this workspace",
    )
    response.headers["Cache-Control"] = "private, no-store"
    wanted = {(ref.artifact_id, ref.file_id) for ref in body.files}
    rows = (
        await session.execute(
            select(ArtifactFile.id, ArtifactFile.artifact_id, ArtifactFile.mime_type)
            .join(Artifact, Artifact.id == ArtifactFile.artifact_id)
            .where(
                Artifact.workspace_id == workspace_id,
                or_(
                    *(
                        (ArtifactFile.artifact_id == artifact_id)
                        & (ArtifactFile.id == file_id)
                        for artifact_id, file_id in wanted
                    )
                ),
            )
        )
    ).all()
    links = []
    for file_id, artifact_id, mime_type in sorted(rows, key=lambda row: row[0]):
        if not _is_media(mime_type):
            continue
        claim = new_claim(
            user_id=str(auth.user.id),
            workspace_id=workspace_id,
            artifact_id=artifact_id,
            file_id=file_id,
        )
        token = issue_media_token(claim, secret_key=app_config.SECRET_KEY)
        links.append(
            MediaLinkRead(
                artifact_id=artifact_id,
                file_id=file_id,
                url=f"{PUBLIC_MEDIA_PATH}/{token}",
                mime_type=mime_type,
                expires_at=datetime.fromtimestamp(claim.expires_at, UTC),
            )
        )
    return MediaLinksRead(links=links)


_NOT_FOUND = HTTPException(status_code=404, detail="Media not found")


@router.get("/public/artifact-media/{token}")
async def stream_linked_media(
    token: str,
    request: Request,
    session: AsyncSession = Depends(get_async_session),
) -> Response:
    """Every failure is the same 404, so a link reveals nothing it does not grant."""
    claim = read_media_token(token, secret_key=app_config.SECRET_KEY)
    if claim is None:
        raise _NOT_FOUND
    try:
        user = await session.get(User, uuid.UUID(claim.user_id))
    except ValueError:
        raise _NOT_FOUND from None
    if user is None or not user.is_active:
        raise _NOT_FOUND
    try:
        await check_permission(
            session,
            AuthContext.session(user),
            claim.workspace_id,
            Permission.ARTIFACTS_READ.value,
        )
    except HTTPException:
        raise _NOT_FOUND from None
    record = await session.scalar(
        select(ArtifactFile)
        .join(Artifact, Artifact.id == ArtifactFile.artifact_id)
        .where(
            ArtifactFile.id == claim.file_id,
            ArtifactFile.artifact_id == claim.artifact_id,
            Artifact.workspace_id == claim.workspace_id,
        )
    )
    if record is None or not _is_media(record.mime_type):
        raise _NOT_FOUND

    from app.routes.artifacts_routes import artifact_file_response

    media = artifact_file_response(record, request)
    media.headers["Referrer-Policy"] = "no-referrer"
    return media
