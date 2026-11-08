"""Granting the signup credit, once per person."""

from __future__ import annotations

import logging
from typing import Any

from sqlalchemy.dialects.postgresql import insert as postgres_insert
from sqlalchemy.ext.asyncio import AsyncSession

from .identity.registry import identities_of
from .persistence.models import SignupCreditClaim

logger = logging.getLogger(__name__)


async def award_signup_credit(session: AsyncSession, user: Any) -> int:
    """Credit a new account, once per person. Returns the micros granted.

    The amount is the billing setting ``signup_bonus_micros`` (admin page);
    it lands in the top-up bucket through the billing ledger. Every new
    account also gets its plan's monthly credits when its billing account
    opens, so the bonus defaults to nothing.
    """
    from app.billing import ledger
    from app.billing.settings_store import load_settings

    granted = int((await load_settings(session)).get("signup_bonus_micros") or 0)
    if granted <= 0:
        return 0
    if not await _try_claim(session, user):
        return 0

    account, locked_user = await ledger.lock_account(session, user.id)
    await ledger.post(
        session,
        account,
        locked_user,
        bucket=ledger.TOPUP,
        delta=granted,
        kind="signup",
        reason="Signup credit",
        reference=f"signup:{user.id}",
    )
    return granted


async def _try_claim(session: AsyncSession, user: Any) -> bool:
    """Claim this user's identities. False if any of them claimed before."""
    identities = identities_of(user)
    if not identities:
        logger.warning(
            "No identity source recognised user %s; signup credit is ungated.",
            getattr(user, "id", "<unknown>"),
        )
        return True

    # Single statement: concurrent signups on one identity must not both win.
    claimed = await session.execute(
        postgres_insert(SignupCreditClaim)
        .values(
            [
                {
                    "identity_kind": identity.kind,
                    "identity_fingerprint": identity.fingerprint,
                }
                for identity in identities
            ]
        )
        .on_conflict_do_nothing(
            index_elements=["identity_kind", "identity_fingerprint"]
        )
        .returning(SignupCreditClaim.id)
    )

    return len(claimed.scalars().all()) == len(identities)
