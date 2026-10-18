"""Which model (and fallback) a Studio output type runs on in a workspace.

Precedence: the output type's override while its primary model is usable,
then the workspace default, then Auto. A configured model that has been
disabled or removed never fails a generation: it is skipped, logged, and
reported as unavailable on the settings page.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Literal

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Workspace

from .catalog import StudioModel, named_model, usable_model
from .choices import StudioChoice, StudioModelSettings
from .model import WorkspaceStudioModel
from .recommended import RECOMMENDED_DEFAULT, RECOMMENDED_OVERRIDES

logger = logging.getLogger(__name__)

PlanSource = Literal["override", "default", "auto"]


@dataclass(frozen=True)
class StudioPlan:
    """``primary`` None means Auto: the chat model, exactly as before."""

    primary: StudioModel | None
    fallback: StudioModel | None
    source: PlanSource


@dataclass(frozen=True)
class _Resolved:
    primary: StudioModel | None
    fallback: StudioModel | None
    unavailable: bool


async def recommended_settings(
    session: AsyncSession, workspace: Workspace
) -> StudioModelSettings:
    """The recommended setup, with its per-type models as this workspace's ids."""
    overrides: dict[str, StudioChoice] = {}
    for slot, (primary_name, fallback_name) in RECOMMENDED_OVERRIDES.items():
        primary = await named_model(session, workspace, primary_name)
        if primary is None:
            continue
        fallback = (
            await named_model(session, workspace, fallback_name)
            if fallback_name
            else None
        )
        overrides[slot] = StudioChoice(
            mode="model", primary=primary.id, fallback=fallback.id if fallback else None
        )
    return StudioModelSettings(
        default=StudioChoice(mode="recommended"), overrides=overrides
    )


async def load_settings(
    session: AsyncSession, workspace: Workspace
) -> tuple[StudioModelSettings, bool]:
    """The workspace's settings and whether it has changed the recommended setup."""
    row = await session.scalar(
        select(WorkspaceStudioModel).where(
            WorkspaceStudioModel.workspace_id == workspace.id
        )
    )
    if row is None:
        return await recommended_settings(session, workspace), False
    try:
        return (
            StudioModelSettings.model_validate(
                {"default": row.default_choice, "overrides": row.overrides or {}}
            ),
            True,
        )
    except ValueError:
        logger.warning(
            "Studio model settings for workspace %s are unreadable; using Auto",
            workspace.id,
        )
        return StudioModelSettings(default=StudioChoice(mode="auto")), True


async def _resolve(
    session: AsyncSession, workspace: Workspace, choice: StudioChoice
) -> _Resolved:
    if choice.mode == "auto":
        return _Resolved(None, None, False)
    if choice.mode == "recommended":
        primary_name, fallback_name = RECOMMENDED_DEFAULT
        primary = await named_model(session, workspace, primary_name)
        fallback = (
            await named_model(session, workspace, fallback_name)
            if fallback_name
            else None
        )
        if primary is None:
            # The fallback alone still beats Auto, but the setup is degraded.
            return _Resolved(fallback, None, True)
        return _Resolved(primary, fallback, fallback_name is not None and not fallback)
    primary = await usable_model(session, workspace, choice.primary)
    fallback = await usable_model(session, workspace, choice.fallback)
    if primary is None:
        return _Resolved(None, None, True)
    return _Resolved(primary, fallback, choice.fallback is not None and not fallback)


async def resolve_plan(
    session: AsyncSession, workspace: Workspace, slot: str
) -> StudioPlan:
    settings, _ = await load_settings(session, workspace)
    override = settings.overrides.get(slot)
    if override is not None:
        if override.mode == "auto":
            return StudioPlan(None, None, "override")
        resolved = await _resolve(session, workspace, override)
        if resolved.primary is not None:
            return StudioPlan(resolved.primary, resolved.fallback, "override")
        logger.info(
            "Studio model for %s in workspace %s is unavailable; using the default",
            slot,
            workspace.id,
        )
    resolved = await _resolve(session, workspace, settings.default)
    if resolved.primary is None:
        return StudioPlan(None, None, "auto")
    return StudioPlan(resolved.primary, resolved.fallback, "default")


async def unavailable_entries(
    session: AsyncSession, workspace: Workspace, settings: StudioModelSettings
) -> list[str]:
    """``"default"`` and the output types whose chosen model cannot be used now."""
    entries = [("default", settings.default), *settings.overrides.items()]
    return [
        key
        for key, choice in entries
        if (await _resolve(session, workspace, choice)).unavailable
    ]
