"""The notebook API, mounted under ``/api/v1``."""

from fastapi import APIRouter

from ..research import academic_routes as research_academic, routes as research
from ..research.thumbnails import route as research_thumbnails
from . import (
    followups,
    notebooks,
    notes,
    output_share,
    output_view,
    report_view,
    search,
    source_guide,
    sources,
    studio,
    threads,
)

router = APIRouter(tags=["notebooks"])
for module in (
    # Before ``notebooks``: its /notebooks/{notebook_id} would catch /search.
    search,
    notebooks,
    sources,
    source_guide,
    notes,
    threads,
    studio,
    report_view,
    output_view,
    output_share,
    followups,
    research,
    research_academic,
    research_thumbnails,
):
    router.include_router(module.router)

__all__ = ["router"]
