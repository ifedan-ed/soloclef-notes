"""The report reading view: its suggestions, starting one, dismissing."""

from __future__ import annotations

import logging

from fastapi import APIRouter, Depends, HTTPException, Response
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.podcasts.service import PodcastError
from app.sandbox import is_sandbox_enabled
from app.users import get_auth_context

from ..access import authorize, authorized_notebook, touch
from ..studio import start
from ..studio.options import AudioRequest
from ..studio.report_view import service
from ..studio.report_view.schemas import SUGGESTION_KINDS
from .studio import _required_permissions

logger = logging.getLogger(__name__)
router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/studio/outputs/{output_id}"


def get_report_dispatchers() -> start.Dispatchers:
    """Its own seam (not the Studio route's), so tests replace only this one."""
    return start.Dispatchers()


def suggestible_kinds() -> list[str]:
    """Without the sandbox only audio can be made, so only audio is suggested."""
    if is_sandbox_enabled():
        return list(SUGGESTION_KINDS)
    return ["audio"]


async def _report(session, notebook, output_id: int) -> service.LoadedReport:
    try:
        return await service.load_report(session, notebook, output_id)
    except service.ReportNotFoundError:
        raise HTTPException(status_code=404, detail="Report not found") from None


@router.get(_BASE + "/report-view", response_model=service.ReportViewRead)
async def get_report_view(
    workspace_id: int,
    notebook_id: int,
    output_id: int,
    response: Response,
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
    report = await _report(session, notebook, output_id)
    response.headers["Cache-Control"] = "private, no-store"
    return await service.report_view(
        session, notebook, report, kinds=suggestible_kinds()
    )


@router.post(
    _BASE + "/report-suggestions/{suggestion_id}/add",
    response_model=service.ReportViewRead,
)
async def add_report_suggestion(
    workspace_id: int,
    notebook_id: int,
    output_id: int,
    suggestion_id: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    dispatchers: start.Dispatchers = Depends(get_report_dispatchers),
):
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    report = await _report(session, notebook, output_id)

    async def authorize_request(request) -> None:
        await authorize(session, auth, workspace_id, *_required_permissions(request))
        if not isinstance(request, AudioRequest) and not is_sandbox_enabled():
            raise HTTPException(
                status_code=503, detail="Generation is not enabled on this server."
            )

    touch(notebook)
    try:
        return await service.add_suggestion(
            session,
            notebook,
            report,
            suggestion_id,
            user_id=auth.user.id,
            dispatchers=dispatchers,
            authorize_request=authorize_request,
        )
    except service.SuggestionNotFoundError:
        raise HTTPException(status_code=404, detail="Suggestion not found") from None
    except (PodcastError, ValueError) as exc:
        await session.rollback()
        logger.warning("Report suggestion could not start", exc_info=True)
        raise HTTPException(status_code=422, detail=str(exc)[:300]) from None


@router.post(
    _BASE + "/report-suggestions/{suggestion_id}/dismiss",
    response_model=service.ReportViewRead,
)
async def dismiss_report_suggestion(
    workspace_id: int,
    notebook_id: int,
    output_id: int,
    suggestion_id: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    notebook = await authorized_notebook(
        session, auth, workspace_id, notebook_id, Permission.CHATS_UPDATE
    )
    report = await _report(session, notebook, output_id)
    try:
        return await service.dismiss_suggestion(
            session, notebook, report, suggestion_id
        )
    except service.SuggestionNotFoundError:
        raise HTTPException(status_code=404, detail="Suggestion not found") from None
