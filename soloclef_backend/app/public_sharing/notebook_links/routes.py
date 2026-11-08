"""Live notebook links over HTTP.

Signed-in routes make, show and stop a notebook's link; the public routes,
under ``/public-notebooks/{token}``, are read-only and take no credentials.
Every public request resolves the token afresh (``scope.resolve_link``), so an
expired, revoked or deleted notebook's link stops working on the next request.
Nothing here writes on a visitor's behalf, starts a job or calls a model.
"""

from __future__ import annotations

import io

from fastapi import APIRouter, Depends, Request, Response
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel
from sqlalchemy.ext.asyncio import AsyncSession

from app.artifacts.persistence import ArtifactFileRole
from app.artifacts.storage import open_artifact_file_stream
from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.notebooks.access import authorized_notebook
from app.public_sharing.expiry import DEFAULT_EXPIRY_DAYS, ExpiryDays
from app.rate_limiter import limiter
from app.routes.artifacts_routes import (
    _artifact_filename,
    _markdown_filename,
    artifact_file_response,
)
from app.routes.document_files_routes import (
    _content_disposition,
    document_file_response,
)
from app.users import get_auth_context

from . import service, view
from .scope import resolve_link

router = APIRouter()

_NOTEBOOK = "/workspaces/{workspace_id}/notebooks/{notebook_id}/public-link"
_PUBLIC = "/public-notebooks/{token}"

# A page view is one JSON read every few seconds while open; a PDF or video
# makes many small range requests, so files get more room.
_PAGE_LIMIT = "90/minute"
_VIEW_LIMIT = "240/minute"
_FILE_LIMIT = "1200/minute"


class NotebookLinkCreate(BaseModel):
    """``expires_in_days`` null means the link never expires."""

    expires_in_days: ExpiryDays | None = DEFAULT_EXPIRY_DAYS
    regenerate: bool = False


class NotebookLinkState(BaseModel):
    link: service.LiveLinkRead | None


class NotebookLinkList(BaseModel):
    links: list[service.LiveLinkRead]


# --- signed in -------------------------------------------------------------


@router.get(_NOTEBOOK, response_model=NotebookLinkState)
async def get_notebook_link(
    workspace_id: int,
    notebook_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    response.headers["Cache-Control"] = "private, no-store"
    return NotebookLinkState(link=await service.get_link(session, auth, notebook))


@router.post(_NOTEBOOK, response_model=service.LiveLinkRead)
async def share_notebook(
    workspace_id: int,
    notebook_id: int,
    body: NotebookLinkCreate | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    body = body or NotebookLinkCreate()
    return await service.share_notebook(
        session, auth, notebook, body.expires_in_days, body.regenerate
    )


@router.delete(_NOTEBOOK, status_code=204)
async def stop_sharing_notebook(
    workspace_id: int,
    notebook_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_READ
    )
    await service.stop_sharing(session, auth, notebook)
    return Response(status_code=204)


@router.get(
    "/workspaces/{workspace_id}/notebook-links", response_model=NotebookLinkList
)
async def list_notebook_links(
    workspace_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    response.headers["Cache-Control"] = "private, no-store"
    return NotebookLinkList(links=await service.list_links(session, auth, workspace_id))


@router.delete("/workspaces/{workspace_id}/notebook-links/{link_id}", status_code=204)
async def revoke_notebook_link(
    workspace_id: int,
    link_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await service.revoke_link(session, auth, workspace_id, link_id)
    return Response(status_code=204)


# --- public: no credentials, read only ---------------------------------------


def _json(payload: dict) -> JSONResponse:
    return JSONResponse(payload, headers={"Cache-Control": "no-store"})


def _public_file(response: Response, inline_safe: bool) -> Response:
    """Revalidate on every use (so a revoked link stops serving), and never let
    a browser run what it was sent: anything but a PDF or MP4 is a sandboxed
    download if opened directly."""
    response.headers["Cache-Control"] = "private, no-cache"
    if not inline_safe:
        response.headers["Content-Security-Policy"] = "sandbox"
    return response


def _inline_safe(mime_type: str | None) -> bool:
    return (mime_type or "").split(";")[0].strip().lower() in {
        "application/pdf",
        "video/mp4",
    }


@router.get(_PUBLIC)
@limiter.limit(_PAGE_LIMIT)
async def read_public_notebook(
    request: Request,
    token: str,
    session: AsyncSession = Depends(get_async_session),
):
    live = await resolve_link(session, token)
    return _json(await view.page(session, live))


@router.get(_PUBLIC + "/artifacts/{artifact_id}/manifest")
@limiter.limit(_VIEW_LIMIT)
async def read_public_artifact_manifest(
    request: Request,
    token: str,
    artifact_id: int,
    session: AsyncSession = Depends(get_async_session),
):
    live = await resolve_link(session, token)
    artifact, document = await view.shared_artifact(session, live, artifact_id)
    return _json(view.artifact_manifest(live.link.share_token, artifact, document))


@router.get(_PUBLIC + "/artifacts/{artifact_id}/files/{file_id}/content")
@limiter.limit(_FILE_LIMIT)
async def stream_public_artifact_file(
    request: Request,
    token: str,
    artifact_id: int,
    file_id: int,
    session: AsyncSession = Depends(get_async_session),
):
    live = await resolve_link(session, token)
    artifact, _ = await view.shared_artifact(session, live, artifact_id)
    record = view.artifact_file(artifact, file_id)
    return _public_file(
        artifact_file_response(record, request), _inline_safe(record.mime_type)
    )


@router.get(_PUBLIC + "/artifacts/{artifact_id}/download")
@limiter.limit(_VIEW_LIMIT)
async def download_public_artifact(
    request: Request,
    token: str,
    artifact_id: int,
    session: AsyncSession = Depends(get_async_session),
):
    """The output's file, named after its title, always as a download."""
    live = await resolve_link(session, token)
    artifact, document = await view.shared_artifact(session, live, artifact_id)
    primary = next(
        (file for file in artifact.files if file.role is ArtifactFileRole.PRIMARY),
        None,
    )
    headers = {
        "Cache-Control": "private, no-store",
        "X-Content-Type-Options": "nosniff",
        "Content-Security-Policy": "sandbox",
    }
    if primary is not None:
        return StreamingResponse(
            open_artifact_file_stream(primary),
            media_type=primary.mime_type or "application/octet-stream",
            headers={
                **headers,
                "Content-Disposition": _content_disposition(
                    _artifact_filename(document.title, primary.original_filename),
                    inline=False,
                ),
            },
        )
    return StreamingResponse(
        io.BytesIO((document.source_markdown or document.content or "").encode()),
        media_type="text/markdown; charset=utf-8",
        headers={
            **headers,
            "Content-Disposition": _content_disposition(
                _markdown_filename(document.title), inline=False
            ),
        },
    )


@router.get(_PUBLIC + "/sources/{document_id}")
@limiter.limit(_VIEW_LIMIT)
async def read_public_source(
    request: Request,
    token: str,
    document_id: int,
    session: AsyncSession = Depends(get_async_session),
):
    live = await resolve_link(session, token)
    return _json(await view.source_view(session, live, document_id))


@router.get(_PUBLIC + "/sources/{document_id}/files/{file_id}/content")
@limiter.limit(_FILE_LIMIT)
async def stream_public_source_file(
    request: Request,
    token: str,
    document_id: int,
    file_id: int,
    session: AsyncSession = Depends(get_async_session),
):
    live = await resolve_link(session, token)
    record = await view.source_file(session, live, document_id, file_id)
    return _public_file(
        document_file_response(record, request), _inline_safe(record.mime_type)
    )
