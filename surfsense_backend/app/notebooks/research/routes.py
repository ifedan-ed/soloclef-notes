"""HTTP door for notebook web research: stream a search, import picked results."""

from __future__ import annotations

import asyncio
import json
from collections.abc import AsyncIterator, Mapping

from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import StreamingResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.capabilities.core import Executor
from app.capabilities.core.access.rate_limit import enforce_capability_rate_limit
from app.db import Permission, get_async_session
from app.users import get_auth_context
from app.utils.crawl import is_publicly_routable

from ..access import authorized_notebook, touch
from ..schemas import SourceRead
from ..sources_listing import list_sources
from .academic.clients import AcademicClients
from .capability_runner import CapabilityRunner
from .import_service import import_items
from .multi_run_log import logged_multi_research_events
from .multi_search import multi_research_events
from .page_reader import read_page
from .run_log import RunRecorder, logged_research_events
from .schemas import (
    ImportItem,
    ImportRequest,
    ReadPageRequest,
    ResearchSource,
    ResearchSourcesStatus,
    SearchRequest,
)
from .seams import (
    ImportDispatcher,
    get_academic_clients,
    get_capability_executors,
    get_import_dispatcher,
    get_page_read_dispatcher,
    get_run_recorder,
    get_searxng_page,
    get_web_api_sender,
)
from .search import research_events
from .web_apis.keys import browserbase_key
from .web_apis.requests import ApiSender
from .web_apis.search import WebApiSearch
from .web_search import SearxngPage

router = APIRouter()

_BASE = "/workspaces/{workspace_id}/notebooks/{notebook_id}/research"
_SSE_HEADERS = {"Cache-Control": "no-cache", "X-Accel-Buffering": "no"}


def _sse(event: dict) -> str:
    return f"data: {json.dumps(event, default=str)}\n\n"


@router.post(_BASE + "/search", dependencies=[Depends(enforce_capability_rate_limit)])
async def search(
    workspace_id: int,
    notebook_id: int,
    body: SearchRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    executors: Mapping[str, Executor] = Depends(get_capability_executors),
    searxng_fetch: SearxngPage = Depends(get_searxng_page),
    record: RunRecorder = Depends(get_run_recorder),
    academic: AcademicClients = Depends(get_academic_clients),
    send_api: ApiSender = Depends(get_web_api_sender),
):
    """Server-sent events: phase updates, then the results, then done (or an error).

    With several ``sources`` the sources are searched side by side: each
    reports its own progress (``provider`` events), and the results arrive
    merged, each row naming the source that found it.
    Every search is also recorded in the workspace's runs log.
    """
    await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    web_apis = WebApiSearch.for_workspace(session, workspace_id, send_api)
    if len(body.selected) > 1:
        events = _several_sources_events(
            body,
            session=session,
            workspace_id=workspace_id,
            notebook_id=notebook_id,
            user_id=auth.user.id,
            executors=executors,
            searxng_fetch=searxng_fetch,
            record=record,
            academic=academic,
            web_apis=web_apis,
        )
        return _event_stream(events)

    runner = CapabilityRunner(session, workspace_id, executors)
    events = logged_research_events(
        research_events(
            body,
            runner,
            _page_reader(runner),
            searxng_fetch,
            academic,
            web_apis,
        ),
        request=body,
        record=record,
        workspace_id=workspace_id,
        user_id=auth.user.id,
        notebook_id=notebook_id,
        cost_micros=lambda: runner.charged_micros,
    )
    return _event_stream(events)


def _page_reader(runner: CapabilityRunner):
    """Deep research's page reader, billed to ``runner``."""

    async def read(url: str) -> tuple[str | None, str | None]:
        markdown, metadata = await read_page(runner, url)
        return markdown, (metadata or {}).get("image")

    return read


def _several_sources_events(
    body: SearchRequest,
    *,
    session: AsyncSession,
    workspace_id: int,
    notebook_id: int,
    user_id,
    executors: Mapping[str, Executor],
    searxng_fetch: SearxngPage,
    record: RunRecorder,
    academic: AcademicClients,
    web_apis: WebApiSearch,
) -> AsyncIterator[dict]:
    """A search of several sources: one runner (one bill) per source, one session between them."""
    db_lock = asyncio.Lock()
    runners: dict[ResearchSource, CapabilityRunner] = {
        source: CapabilityRunner(session, workspace_id, executors, db_lock=db_lock)
        for source in body.selected
    }
    return logged_multi_research_events(
        multi_research_events(
            body,
            runners,
            lambda source: _page_reader(runners[source]),
            searxng_fetch,
            academic,
            web_apis,
        ),
        request=body,
        record=record,
        workspace_id=workspace_id,
        user_id=user_id,
        notebook_id=notebook_id,
        cost_micros_for=lambda source: runners[source].charged_micros,
    )


def _event_stream(events: AsyncIterator[dict]) -> StreamingResponse:
    async def stream() -> AsyncIterator[str]:
        async for event in events:
            yield _sse(event)

    return StreamingResponse(
        stream(), media_type="text/event-stream", headers=_SSE_HEADERS
    )


@router.get(_BASE + "/sources", response_model=ResearchSourcesStatus)
async def source_status(
    workspace_id: int,
    notebook_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Which key-backed sources (Exa, Tavily) this workspace can search.

    Only whether a key exists is told, never the key.
    """
    await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_READ,
        Permission.DOCUMENTS_READ,
    )
    available = await WebApiSearch.for_workspace(session, workspace_id).availability()
    return ResearchSourcesStatus(
        unavailable={
            source: "not_connected" for source, ok in available.items() if not ok
        },
        page_reader="browserbase"
        if await browserbase_key(session, workspace_id)
        else "crawler",
    )


@router.post(_BASE + "/import", response_model=list[SourceRead])
async def import_results(
    workspace_id: int,
    notebook_id: int,
    body: ImportRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    dispatch: ImportDispatcher = Depends(get_import_dispatcher),
):
    """Add the picked results as pending sources; their pages are read in the background."""
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_UPDATE,
        Permission.DOCUMENTS_CREATE,
        Permission.DOCUMENTS_READ,
    )
    created = await import_items(
        session, notebook=notebook, items=body.items, user_id=auth.user.id
    )
    touch(notebook)
    await session.commit()
    if created:
        dispatch(created, workspace_id, auth.user.id)
    return await list_sources(session, notebook)


@router.post(
    _BASE + "/read-page",
    response_model=list[SourceRead],
    dependencies=[Depends(enforce_capability_rate_limit)],
)
async def add_web_page(
    workspace_id: int,
    notebook_id: int,
    body: ReadPageRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    dispatch: ImportDispatcher = Depends(get_page_read_dispatcher),
):
    """Add a pasted web page as a source; it is read in the background.

    Browserbase renders it when the workspace has it connected (JavaScript-
    heavy pages work), otherwise the crawler reads it. Only public http(s)
    addresses are accepted: the host must resolve to public addresses only.
    A page the workspace already holds is linked again, not read twice.
    """
    notebook = await authorized_notebook(
        session,
        auth,
        workspace_id,
        notebook_id,
        Permission.CHATS_UPDATE,
        Permission.DOCUMENTS_CREATE,
        Permission.DOCUMENTS_READ,
    )
    if not await asyncio.to_thread(is_publicly_routable, body.url):
        raise HTTPException(
            status_code=422,
            detail="That address isn't a public web page. Paste an http or "
            "https link to a site on the internet.",
        )
    created = await import_items(
        session,
        notebook=notebook,
        items=[ImportItem(url=body.url, title=body.url, source="web")],
        user_id=auth.user.id,
    )
    touch(notebook)
    await session.commit()
    if created:
        dispatch(created, workspace_id, auth.user.id)
    return await list_sources(session, notebook)
