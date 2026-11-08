"""HTTP door for one academic-source search, with no notebook required.

Reuses the same academic-source functions notebook research search uses
(``search_academic``), without creating a notebook or an import. Built for the
MCP server's standalone academic search tools (``soloclef_pubmed_search`` and
siblings). Academic search is not billed today (see ``academic/search.py``,
which calls no credit service) — this route keeps that unchanged.
"""

from __future__ import annotations

from typing import Literal

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field, field_validator
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.capabilities.core.access.rate_limit import enforce_capability_rate_limit
from app.db import Permission, get_async_session
from app.users import get_auth_context

from ..access import authorize
from .academic.clients import AcademicClients
from .academic.search import FAST_LIMIT, search_academic
from .schemas import ResultRow
from .seams import get_academic_clients

router = APIRouter()

MAX_RESULTS = 30
AcademicSource = Literal["pubmed", "arxiv", "medrxiv", "google_scholar"]


class AcademicSearchRequest(BaseModel):
    """One query against one academic source, standalone (no notebook)."""

    query: str = Field(min_length=1, max_length=500)
    max_results: int = Field(default=FAST_LIMIT, ge=1, le=MAX_RESULTS)
    # PubMed only: widen to PMC open-access full-text excerpts on top hits.
    full_text: bool = False
    # PubMed only: restrict to a publication-year window.
    since: int | None = Field(default=None, ge=1000, le=3000)
    until: int | None = Field(default=None, ge=1000, le=3000)
    # medrxiv source only: which of the two servers to keep.
    server: Literal["medrxiv", "biorxiv"] | None = None

    @field_validator("query", mode="before")
    @classmethod
    def _strip(cls, value: object) -> object:
        return value.strip() if isinstance(value, str) else value


def _pubmed_date_filter(query: str, since: int | None, until: int | None) -> str:
    if not since and not until:
        return query
    lo = since or 1000
    hi = until or 3000
    return f'{query} AND ("{lo}"[pdat] : "{hi}"[pdat])'


@router.post(
    "/workspaces/{workspace_id}/academic-search/{source}",
    response_model=list[ResultRow],
    dependencies=[Depends(enforce_capability_rate_limit)],
)
async def academic_search(
    workspace_id: int,
    source: AcademicSource,
    body: AcademicSearchRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    academic: AcademicClients = Depends(get_academic_clients),
) -> list[ResultRow]:
    """Search one academic source and return its result rows.

    Requires CHATS_READ and DOCUMENTS_READ on the workspace (same as notebook
    research search); shares its per-workspace rate limit.
    """
    await authorize(
        session, auth, workspace_id, Permission.CHATS_READ, Permission.DOCUMENTS_READ
    )

    query = body.query
    if source == "pubmed":
        query = _pubmed_date_filter(query, body.since, body.until)

    # Ask for the full (deep) pool whenever we need more candidates than the
    # fast page gives: PubMed full-text mode, a server-filtered preprint
    # search (half the fast pool would otherwise be thrown away), or a
    # max_results above the fast limit.
    deep = (
        (source == "pubmed" and body.full_text)
        or (source == "medrxiv" and body.server is not None)
        or body.max_results > FAST_LIMIT
    )

    rows = await search_academic(source, query, deep=deep, clients=academic)

    if source == "medrxiv" and body.server:
        rows = [row for row in rows if row.paper and row.paper.server == body.server]

    return rows[: body.max_results]
