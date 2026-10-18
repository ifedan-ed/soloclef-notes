"""Which models are premium.

A model's tier is ``standard`` or ``premium``. Admins set it per model id
(the LiteLLM model name, e.g. ``ds-deepseek-v4.1-flash``) on the billing admin
page; that choice is stored in the ``model_tiers`` billing setting and mirrored
onto every ``models.billing_tier`` row with that model id. Without an admin
choice, a database model's own ``billing_tier`` column applies, and a global
(YAML) model's ``billing_tier: premium`` does. Everything else is standard.
"""

from __future__ import annotations

from typing import Any

from sqlalchemy import select, update
from sqlalchemy.ext.asyncio import AsyncSession

from .settings_store import load_settings, save_settings

STANDARD = "standard"
PREMIUM = "premium"
SETTING_KEY = "model_tiers"


def normalize(tier: Any) -> str:
    return PREMIUM if str(tier or "").lower() == PREMIUM else STANDARD


async def tier_overrides(session: AsyncSession) -> dict[str, str]:
    settings = await load_settings(session)
    value = settings.get(SETTING_KEY) or {}
    return (
        {str(k): normalize(v) for k, v in value.items()}
        if isinstance(value, dict)
        else {}
    )


async def tier_for(
    session: AsyncSession,
    *,
    config_id: int | None,
    model_name: str | None,
    yaml_tier: str | None = None,
) -> str:
    overrides = await tier_overrides(session)
    if model_name and model_name in overrides:
        return overrides[model_name]
    if config_id and config_id > 0:
        from app.db import Model

        column = await session.scalar(
            select(Model.billing_tier).where(Model.id == config_id)
        )
        return normalize(column)
    return normalize(yaml_tier)


async def set_tier(
    session: AsyncSession, model_name: str, tier: str, actor=None
) -> None:
    from app.db import Model

    tier = normalize(tier)
    overrides = await tier_overrides(session)
    overrides[model_name] = tier
    await save_settings(session, values={SETTING_KEY: overrides}, actor=actor)
    await session.execute(
        update(Model).where(Model.model_id == model_name).values(billing_tier=tier)
    )
