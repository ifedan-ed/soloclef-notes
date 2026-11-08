"""Which plan answers each research source."""

from __future__ import annotations

from ..schemas import ResearchSource
from . import (
    amazon,
    google_maps,
    indeed,
    instagram,
    reddit,
    serp,
    tiktok,
    walmart,
    youtube,
)
from .plan import SourcePlan

# "web" also maps to the Google plan: it is the fallback when SearXNG is unset.
PLANS: dict[ResearchSource, SourcePlan] = {
    "web": serp.PLAN,
    "google_search": serp.PLAN,
    "youtube": youtube.PLAN,
    "reddit": reddit.PLAN,
    "instagram": instagram.PLAN,
    "tiktok": tiktok.PLAN,
    "google_maps": google_maps.PLAN,
    "indeed": indeed.PLAN,
    "amazon": amazon.PLAN,
    "walmart": walmart.PLAN,
}


def plan_for(source: ResearchSource) -> SourcePlan:
    return PLANS[source]
