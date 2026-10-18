"""Charge an automation's agent step like a chat turn.

One ``agent_task`` step is one agent turn, so it is priced exactly as a chat
message on the same model: ``chat_standard`` or ``chat_premium`` by the
model's billing tier (``billing_settings.model_tiers``), through the same
meter (``premium_quota.reserve_credit`` -> ``meter.begin``). The hold is taken
before the model runs, settled when the turn finishes (whatever the agent
concluded: the model did the work) and released if the turn raised, timed out
or was cancelled. Shadow mode only records; exempt accounts are recorded as
exempt and never charged; in live mode a plan without the model's tier, or an
empty wallet, fails the step with the reason before any model call.

The creator of the automation pays, as they would for the chat message.
"""

from __future__ import annotations

import logging
from typing import Any

logger = logging.getLogger(__name__)

# BillingDenied codes -> what the run shows its owner.
_DENIAL_REASONS = {
    "INSUFFICIENT_CREDITS": (
        "Not enough credits to run this step. Top up or wait for your plan's "
        "credits to renew."
    ),
    "PLAN_MODEL_TIER_NOT_INCLUDED": (
        "Your plan doesn't include this step's model. Choose a standard model "
        "or Auto for the automation, or upgrade."
    ),
}


class AutomationBillingDeniedError(Exception):
    """The meter refused the step (live mode); carries the billing code."""

    def __init__(self, code: str | None):
        self.code = code or "billing_denied"
        super().__init__(
            _DENIAL_REASONS.get(self.code, f"Billing refused this step ({self.code}).")
        )


async def hold_step_credit(
    *,
    agent_config: Any,
    user_id: str | None,
    workspace_id: int,
    session_factory=None,
):
    """Hold the step's price. Returns the reservation (``None`` when there is
    no one to bill); raises :class:`AutomationBillingDeniedError` when refused."""
    from app.tasks.chat.streaming.flows.shared.premium_quota import (
        needs_credit_quota,
        reserve_credit,
    )

    if not needs_credit_quota(agent_config, user_id):
        return None
    reservation = await reserve_credit(
        agent_config=agent_config,
        user_id=str(user_id),
        workspace_id=workspace_id,
        session_factory=session_factory,
    )
    if not reservation.allowed:
        raise AutomationBillingDeniedError(reservation.error_code)
    return reservation


async def settle_step_credit(
    reservation, *, details: dict[str, Any] | None = None, session_factory=None
) -> None:
    """Debit the hold. Never raises: the run's result is already in hand."""
    if reservation is None or reservation.charge is None:
        return
    try:
        from app.billing import meter

        await meter.settle(
            reservation.charge, details=details, session_factory=session_factory
        )
    except Exception:
        logger.warning("settling automation credit failed", exc_info=True)


async def release_step_credit(
    reservation, *, reason: str, session_factory=None
) -> None:
    """Give the hold back. Never raises."""
    if reservation is None or reservation.charge is None:
        return
    try:
        from app.billing import meter

        await meter.release(
            reservation.charge, reason=reason, session_factory=session_factory
        )
    except Exception:
        logger.warning("releasing automation credit failed", exc_info=True)
