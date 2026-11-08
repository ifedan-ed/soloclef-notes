"""Credit for a chat turn: hold the turn's price, then settle or release it.

Every authenticated turn is metered through ``app.billing.meter`` at the flat
per-turn price of its model's tier (``chat_standard`` / ``chat_premium``,
admin-editable). ``stream_new_chat`` and ``stream_resume_chat`` reserve up
front, settle when the turn completes, and release on the cancellation /
interrupted paths.

In live billing mode a Free-plan user asking a premium model, or anyone out of
credit, is refused before the model runs: ``reserve_credit`` returns
``allowed=False`` with the billing ``error_code`` for the SSE error frame. In
shadow mode the turn is only recorded with what it would have cost.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import TYPE_CHECKING

from app.agents.chat.runtime.llm_config import AgentConfig

if TYPE_CHECKING:
    from app.billing.meter import Charge
    from app.services.token_tracking_service import TokenAccumulator

logger = logging.getLogger(__name__)


@dataclass
class CreditReservation:
    """Active credit hold for one turn.

    ``request_id`` is the billing usage id (``""`` when billing is off).
    ``error_code`` says why a refused turn was refused.
    """

    request_id: str
    reserved_micros: int
    allowed: bool
    charge: Charge | None = None
    error_code: str | None = None


def needs_credit_quota(agent_config: AgentConfig | None, user_id: str | None) -> bool:
    return bool(agent_config is not None and user_id)


async def turn_action(
    agent_config: AgentConfig, session_factory=None
) -> tuple[str, str]:
    """``(action, tier)`` a turn on this model is priced as."""
    from app.billing import meter, model_tiers

    async with (session_factory or meter._default_factory())() as session:
        tier = await model_tiers.tier_for(
            session,
            config_id=agent_config.config_id,
            model_name=agent_config.model_name,
            yaml_tier=agent_config.billing_tier,
        )
    action = "chat_premium" if tier == model_tiers.PREMIUM else "chat_standard"
    return action, tier


async def reserve_credit(
    *,
    agent_config: AgentConfig,
    user_id: str,
    workspace_id: int | None = None,
    thread_id: int | None = None,
    session_factory=None,
) -> CreditReservation:
    """Hold the turn's price; a refusal comes back as ``allowed=False``."""
    from app.billing import meter
    from app.billing.entitlements import Requirement
    from app.billing.errors import BillingDenied

    action, tier = await turn_action(agent_config, session_factory)
    try:
        charge = await meter.begin(
            user_id,
            action,
            need=Requirement(model_tier=tier),
            workspace_id=workspace_id,
            thread_id=thread_id,
            details={"model": agent_config.model_name or ""},
            session_factory=session_factory,
        )
    except BillingDenied as exc:
        return CreditReservation(
            request_id="",
            reserved_micros=0,
            allowed=False,
            error_code=exc.code,
        )
    return CreditReservation(
        request_id=str(charge.usage_id or ""),
        reserved_micros=charge.micros,
        allowed=True,
        charge=charge,
    )


async def finalize_credit(
    *,
    reservation: CreditReservation,
    user_id: str,
    accumulator: TokenAccumulator,
    session_factory=None,
) -> None:
    """Settle the turn. Never raises: the user already has their answer."""
    try:
        from app.billing import meter

        await meter.settle(
            reservation.charge,
            details={
                "provider_cost_micros": int(accumulator.total_cost_micros or 0),
                "tokens": int(accumulator.grand_total or 0),
            },
            session_factory=session_factory,
        )
    except Exception:
        logger.warning(
            "Failed to settle chat credit for user %s", user_id, exc_info=True
        )


async def release_credit(
    *,
    reservation: CreditReservation,
    user_id: str,
    session_factory=None,
) -> None:
    """Drop the hold on cancellation paths; never raises."""
    try:
        from app.billing import meter

        await meter.release(
            reservation.charge,
            reason="turn not completed",
            session_factory=session_factory,
        )
    except Exception:
        logger.warning("Failed to release chat credit for user %s", user_id)
