"""What a plan allows: pure decisions over a plan's ``entitlements`` JSON.

Keys (every plan carries them; a missing key reads as the Free default):
    parsers               ["local", "cloud"]
    vision                bool
    model_tiers           ["standard", "premium"]
    studio_kinds          Studio output kinds the plan may start
    max_video_seconds     0 = no video; never above the video engine's own
                          maximum (VIDEO_SPEC.max_duration_seconds)
    max_pages_per_upload  0 = unlimited
    max_concurrent_jobs   Studio jobs running at once, 0 = unlimited
    max_notebooks         0 = unlimited
    max_sources_per_notebook  0 = unlimited
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from app.deliverables.jobs.policy import VIDEO_SPEC

from .defaults import FREE_ENTITLEMENTS

PARSER_NOT_INCLUDED = "PLAN_PARSER_NOT_INCLUDED"
VISION_NOT_INCLUDED = "PLAN_VISION_NOT_INCLUDED"
MODEL_TIER_NOT_INCLUDED = "PLAN_MODEL_TIER_NOT_INCLUDED"
STUDIO_KIND_NOT_INCLUDED = "PLAN_STUDIO_KIND_NOT_INCLUDED"
VIDEO_TOO_LONG = "PLAN_VIDEO_TOO_LONG"
TOO_MANY_PAGES = "PLAN_TOO_MANY_PAGES"
TOO_MANY_JOBS = "PLAN_TOO_MANY_JOBS"
TOO_MANY_NOTEBOOKS = "PLAN_TOO_MANY_NOTEBOOKS"
TOO_MANY_SOURCES = "PLAN_TOO_MANY_SOURCES"
INSUFFICIENT_CREDITS = "INSUFFICIENT_CREDITS"

ENTITLEMENT_CODES = frozenset(
    {
        PARSER_NOT_INCLUDED,
        VISION_NOT_INCLUDED,
        MODEL_TIER_NOT_INCLUDED,
        STUDIO_KIND_NOT_INCLUDED,
        VIDEO_TOO_LONG,
        TOO_MANY_PAGES,
        TOO_MANY_JOBS,
        TOO_MANY_NOTEBOOKS,
        TOO_MANY_SOURCES,
    }
)


@dataclass(frozen=True)
class Requirement:
    """What one action needs from the plan. ``None`` fields are not checked."""

    parser: str | None = None
    vision: bool = False
    model_tier: str | None = None
    studio_kind: str | None = None
    video_seconds: int | None = None
    pages: int | None = None
    # Filled in by the meter under the account lock, not by callers.
    running_jobs: int | None = None
    notebooks: int | None = None
    sources_in_notebook: int | None = None


def normalized(entitlements: dict[str, Any] | None) -> dict[str, Any]:
    merged = dict(FREE_ENTITLEMENTS)
    merged.update(entitlements or {})
    return merged


def video_seconds_cap(entitlements: dict[str, Any] | None) -> int:
    """The longest video the plan allows, clamped to what the engine renders.

    0 means no video. A plan set above the engine maximum (or a value that is
    not a number) cannot promise a length the executor would refuse.
    """
    try:
        cap = int(normalized(entitlements).get("max_video_seconds") or 0)
    except (TypeError, ValueError):
        return 0
    return max(0, min(cap, VIDEO_SPEC.max_duration_seconds))


def _limit_exceeded(limit: Any, value: int | None) -> bool:
    try:
        cap = int(limit or 0)
    except (TypeError, ValueError):
        return False
    return value is not None and cap > 0 and value > cap


def denial(entitlements: dict[str, Any] | None, need: Requirement) -> str | None:
    """The first rule ``need`` breaks, as an error code; ``None`` if allowed."""
    ent = normalized(entitlements)
    if need.parser is not None and need.parser not in ent["parsers"]:
        return PARSER_NOT_INCLUDED
    if need.vision and not ent["vision"]:
        return VISION_NOT_INCLUDED
    if need.model_tier is not None and need.model_tier not in ent["model_tiers"]:
        return MODEL_TIER_NOT_INCLUDED
    if need.studio_kind is not None and need.studio_kind not in ent["studio_kinds"]:
        return STUDIO_KIND_NOT_INCLUDED
    if need.video_seconds is not None:
        cap = video_seconds_cap(ent)
        if cap <= 0 or need.video_seconds > cap:
            return VIDEO_TOO_LONG
    if _limit_exceeded(ent.get("max_pages_per_upload"), need.pages):
        return TOO_MANY_PAGES
    if need.running_jobs is not None and _limit_exceeded(
        ent.get("max_concurrent_jobs"), need.running_jobs + 1
    ):
        return TOO_MANY_JOBS
    if need.notebooks is not None and _limit_exceeded(
        ent.get("max_notebooks"), need.notebooks + 1
    ):
        return TOO_MANY_NOTEBOOKS
    if need.sources_in_notebook is not None and _limit_exceeded(
        ent.get("max_sources_per_notebook"), need.sources_in_notebook + 1
    ):
        return TOO_MANY_SOURCES
    return None
