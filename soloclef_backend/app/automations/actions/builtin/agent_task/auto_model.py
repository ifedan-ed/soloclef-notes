"""Turn a workspace's "Auto" chat model (id 0) into a concrete model for a run.

The chat pins Auto per thread; an automation run has no thread, so it picks
the same way from the same candidates, seeded by the workspace id so a
workspace's runs keep using one model. It never picks a premium model while a
standard one is available, so an unattended run is not refused (or charged)
as premium on a plan that only has standard.
"""

from __future__ import annotations

from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

AUTO_MODEL_ID = 0


class AutoModelUnavailableError(Exception):
    """No chat model is available to stand in for Auto."""


async def resolve_auto_chat_model(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: UUID | str | None,
    seed: int,
) -> int:
    from app.services import auto_model_pin_service as pins

    candidates = await pins.auto_model_candidates(
        session, workspace_id=workspace_id, user_id=user_id, capability="chat"
    )
    await pins._apply_billing_tiers(session, candidates)
    pool = pins.auto_eligible_candidates(candidates, premium_eligible=True)
    if not pool:
        raise AutoModelUnavailableError("no chat model is available for Auto")
    return int(pins.choose_auto_model_candidate(pool, seed)["id"])
