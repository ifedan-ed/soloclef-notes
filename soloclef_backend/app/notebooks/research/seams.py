"""Swappable boundaries of notebook research, provided as FastAPI dependencies.

Production uses the registered scrapers, the bundled SearXNG and Celery; tests
override these to stand in fakes at the edge without touching the routes.
"""

from __future__ import annotations

from collections.abc import Callable, Mapping
from uuid import UUID

from app.capabilities.core import Executor

from .academic.clients import DEFAULT_CLIENTS, AcademicClients
from .run_log import RunRecorder
from .web_apis.requests import ApiSender
from .web_apis.search import send_json
from .web_search import SearxngPage, searxng_page

# (document ids, workspace id, user id) -> queues their background fetch.
ImportDispatcher = Callable[[list[int], int, UUID], None]


def get_capability_executors() -> Mapping[str, Executor]:
    """Executor overrides by capability name; empty means the registered ones."""
    return {}


def get_searxng_page() -> SearxngPage:
    return searxng_page


def get_academic_clients() -> AcademicClients:
    """PubMed, arXiv, Europe PMC/bioRxiv and Scholar, reached politely."""
    return DEFAULT_CLIENTS


def get_web_api_sender() -> ApiSender:
    """Sends one Exa/Tavily search request; tests stand in the provider here."""
    return send_json


def dispatch_with_celery(
    document_ids: list[int], workspace_id: int, user_id: UUID
) -> None:
    from .import_task import import_research_sources_task

    import_research_sources_task.delay(document_ids, workspace_id, str(user_id))


def get_import_dispatcher() -> ImportDispatcher:
    return dispatch_with_celery


def dispatch_page_read(
    document_ids: list[int], workspace_id: int, user_id: UUID
) -> None:
    from .page_read.task import read_pages_task

    read_pages_task.delay(document_ids, workspace_id, str(user_id))


def get_page_read_dispatcher() -> ImportDispatcher:
    """Queues the read of pasted pages; tests stand in here."""
    return dispatch_page_read


async def record_on_own_session(**kwargs) -> str | None:
    """Write the run on a dedicated session, as the scraper REST door does."""
    from app.capabilities.core.runs import record_run
    from app.db import async_session_maker

    async with async_session_maker() as session:
        return await record_run(session, **kwargs)


def get_run_recorder() -> RunRecorder:
    return record_on_own_session
