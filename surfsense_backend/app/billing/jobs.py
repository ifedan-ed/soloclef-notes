"""Settle or give back a Studio output's credit when its job ends.

Starting an output holds its price under a reference naming the job
(``deliverable_job:<id>`` or ``podcast:<id>``). The job's own lifecycle then
settles the hold when it is ready and releases it when it fails or is
cancelled, so a failed output costs nothing. Every function here is
best-effort: billing trouble never fails the job transition that called it.
"""

from __future__ import annotations

import logging

from sqlalchemy.ext.asyncio import AsyncSession

from . import meter

logger = logging.getLogger(__name__)


def deliverable_reference(job_id: int) -> str:
    return f"deliverable_job:{job_id}"


def podcast_reference(podcast_id: int) -> str:
    return f"podcast:{podcast_id}"


async def settle_reference(session: AsyncSession, reference: str) -> None:
    try:
        await meter.settle(reference, session_factory=meter.sibling_sessions(session))
    except Exception:
        logger.exception("[billing] settling %s failed", reference)


async def release_reference(
    session: AsyncSession, reference: str, reason: str | None = None
) -> None:
    try:
        await meter.release(
            reference, reason=reason, session_factory=meter.sibling_sessions(session)
        )
    except Exception:
        logger.exception("[billing] releasing %s failed", reference)
