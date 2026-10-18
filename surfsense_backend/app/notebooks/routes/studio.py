"""Studio: start generations from a notebook and list what they produced."""

from __future__ import annotations

import contextlib
import logging
from typing import Literal

from fastapi import APIRouter, Body, Depends, HTTPException, Query, Response
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.billing import meter
from app.billing.errors import BillingDenied
from app.billing.studio import begin_studio
from app.db import DeliverableJob, Permission, get_async_session
from app.deliverables.jobs.service import request_deliverable_job_cancellation
from app.podcasts.persistence import Podcast
from app.podcasts.service import PodcastError, PodcastService
from app.sandbox import is_sandbox_enabled
from app.users import get_auth_context

from ..access import authorize, authorized_notebook, touch
from ..output_titles import current_output_title
from ..persistence import NotebookOutput
from ..studio import report_templates, start
from ..studio.listing import StudioOutputRead, list_outputs
from ..studio.options import AudioRequest, StudioRequest, VideoRequest
from ..studio.source_material import resolve_source_documents
from ..studio.suggestions import focus_suggestions

logger = logging.getLogger(__name__)
router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/studio"

# Default titles only when the client sends none; the web sends localized ones.
_DEFAULT_TITLES = {
    "audio": "Audio Overview",
    "video": "Video Overview",
    "report": "Report",
    "slides": "Slide Deck",
    "mindmap": "Mind Map",
    "flashcards": "Flashcards",
    "quiz": "Quiz",
    "data_table": "Data Table",
    "infographic": "Infographic",
}


def _required_permissions(request) -> tuple[Permission, ...]:
    if isinstance(request, AudioRequest):
        return (Permission.PODCASTS_CREATE, Permission.PODCASTS_UPDATE)
    if isinstance(request, VideoRequest):
        return (Permission.VIDEO_PRESENTATIONS_CREATE,)
    return (Permission.ARTIFACTS_CREATE,)


def get_dispatchers() -> start.Dispatchers:
    return start.Dispatchers()


@router.post(_BASE + "/generate", response_model=StudioOutputRead, status_code=201)
async def generate(
    workspace_id: int,
    notebook_id: int,
    request: StudioRequest = Body(...),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    dispatchers: start.Dispatchers = Depends(get_dispatchers),
):
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    await authorize(session, auth, workspace_id, *_required_permissions(request))
    if not isinstance(request, AudioRequest) and not is_sandbox_enabled():
        raise HTTPException(
            status_code=503, detail="Generation is not enabled on this server."
        )
    documents = await resolve_source_documents(session, notebook, request.source_ids)
    title = current_output_title(
        (request.title or "").strip()
        or f"{_DEFAULT_TITLES[request.kind]}: {notebook.title}",
        notebook,
    )
    touch(notebook)
    billing_sessions = meter.sibling_sessions(session)
    # The plan must include this output, and its credits are held until the
    # job ends (settled when ready, given back when it fails).
    try:
        charge, video_cap = await begin_studio(
            auth.user.id,
            request.kind,
            workspace_id=workspace_id,
            notebook_id=notebook_id,
            session_factory=billing_sessions,
        )
    except BillingDenied as exc:
        await session.rollback()
        raise exc.to_http() from None
    try:
        output = await start.start_generation(
            session,
            notebook=notebook,
            request=request,
            title=title[:200],
            documents=documents,
            user_id=auth.user.id,
            dispatchers=dispatchers,
            charge=charge,
            max_video_seconds=video_cap,
        )
    except (PodcastError, ValueError) as exc:
        await session.rollback()
        await meter.release(
            charge, reason="could not start", session_factory=billing_sessions
        )
        logger.warning("Studio generation could not start", exc_info=True)
        raise HTTPException(status_code=422, detail=str(exc)[:300]) from None
    except BaseException:
        await meter.release(
            charge, reason="could not start", session_factory=billing_sessions
        )
        raise
    items = await list_outputs(session, notebook, output_id=output.id)
    return items[0]


@router.get(_BASE + "/outputs", response_model=list[StudioOutputRead])
async def get_outputs(
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
    return await list_outputs(session, notebook)


class OutputRename(BaseModel):
    title: str = Field(min_length=1, max_length=200)


async def _load_output(session, notebook, output_id: int) -> NotebookOutput:
    output = await session.scalar(
        select(NotebookOutput).where(
            NotebookOutput.id == output_id,
            NotebookOutput.notebook_id == notebook.id,
        )
    )
    if output is None:
        raise HTTPException(status_code=404, detail="Output not found")
    return output


@router.patch(_BASE + "/outputs/{output_id}", response_model=StudioOutputRead)
async def rename_output(
    workspace_id: int,
    notebook_id: int,
    output_id: int,
    body: OutputRename,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    output = await _load_output(session, notebook, output_id)
    output.title = body.title.strip()
    await session.commit()
    return (await list_outputs(session, notebook, output_id=output.id))[0]


@router.delete(_BASE + "/outputs/{output_id}", status_code=204)
async def delete_output(
    workspace_id: int,
    notebook_id: int,
    output_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Stop the generation if it is still running and delete what it produced."""
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    output = await _load_output(session, notebook, output_id)
    artifact_id = None
    if output.deliverable_job_id is not None:
        job = await session.get(DeliverableJob, output.deliverable_job_id)
        if job is not None and job.workspace_id == workspace_id:
            artifact_id = job.artifact_id
            await request_deliverable_job_cancellation(session, job.id)
    if output.podcast_id is not None:
        podcast = await session.get(Podcast, output.podcast_id)
        if podcast is not None and podcast.workspace_id == workspace_id:
            artifact_id = podcast.artifact_id
            with contextlib.suppress(PodcastError):
                await PodcastService(session).cancel(podcast)
    if artifact_id is not None:
        await authorize(session, auth, workspace_id, Permission.ARTIFACTS_DELETE)
    await session.delete(output)
    await session.commit()
    if artifact_id is not None:
        from app.routes import artifacts_routes

        with contextlib.suppress(HTTPException):
            await artifacts_routes.delete_artifact(
                workspace_id, artifact_id, session, auth
            )
    return Response(status_code=204)


class SuggestionsRead(BaseModel):
    # The three to show first and an example for the topic box; the dialog
    # rotates through ``pool`` and ``examples`` on later opens.
    suggestions: list[str]
    placeholder: str | None = None
    pool: list[str] = []
    examples: list[str] = []


@router.get(_BASE + "/suggestions", response_model=SuggestionsRead)
async def get_suggestions(
    workspace_id: int,
    notebook_id: int,
    kind: str | None = None,
    variant: str | None = Query(default=None, max_length=32),
    language: str | None = Query(default=None, max_length=35),
    locale: str | None = Query(default=None, max_length=35),
    refresh: bool = False,
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
    try:
        documents = await resolve_source_documents(session, notebook, None)
    except HTTPException:
        documents = []
    result = await focus_suggestions(
        session,
        notebook,
        documents,
        kind=kind,
        variant=variant,
        language=language,
        locale=locale,
        refresh=refresh,
    )
    return SuggestionsRead(
        suggestions=result.suggestions,
        placeholder=result.placeholder,
        pool=result.pool,
        examples=result.examples,
    )


class ReportTemplateRead(BaseModel):
    key: str
    format: Literal["interactive", "document"]
    title: str
    description: str
    prompt: str
    customized: bool


class ReportTemplateUpdate(BaseModel):
    title: str | None = Field(default=None, max_length=200)
    description: str | None = Field(default=None, max_length=500)
    prompt: str = Field(min_length=1, max_length=4000)


_TEMPLATES = "/workspaces/{workspace_id}/notebook-report-templates"


@router.get(_TEMPLATES, response_model=list[ReportTemplateRead])
async def get_report_templates(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await authorize(session, auth, workspace_id, Permission.CHATS_READ)
    return [
        ReportTemplateRead(**template.__dict__)
        for template in await report_templates.list_templates(session, workspace_id)
    ]


@router.put(_TEMPLATES + "/{template_key}", response_model=list[ReportTemplateRead])
async def update_report_template(
    workspace_id: int,
    template_key: str,
    body: ReportTemplateUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await authorize(session, auth, workspace_id, Permission.CHATS_UPDATE)
    try:
        await report_templates.save_template(
            session,
            workspace_id,
            template_key,
            title=body.title,
            description=body.description,
            prompt=body.prompt,
        )
    except KeyError:
        raise HTTPException(status_code=404, detail="Template not found") from None
    return await get_report_templates(workspace_id, session, auth)


@router.delete(_TEMPLATES + "/{template_key}", response_model=list[ReportTemplateRead])
async def reset_report_template(
    workspace_id: int,
    template_key: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    await authorize(session, auth, workspace_id, Permission.CHATS_UPDATE)
    if report_templates.seed_for(template_key) is None:
        raise HTTPException(status_code=404, detail="Template not found")
    await report_templates.reset_template(session, workspace_id, template_key)
    return await get_report_templates(workspace_id, session, auth)
