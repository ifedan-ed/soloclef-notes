"""Charge the workspace owner per billable success at the capability executor (03c).

Routes every scrape/crawl meter through ``app.billing.meter`` -- the same
check -> run -> record shape ``EtlCreditService`` uses for upload pages
(``gate_capability`` is ``meter.check``'s worst-case pre-flight, never
holding; ``charge_capability`` is ``meter.record``'s post-charge for the
actual count, which never refuses because the check already ran). Before
c0021 this went straight at ``User.credit_micros_balance``
(``app/services/wallet_credit.py``, now removed): no plan rules, no
``billing_usage`` audit row, and the payer was resolved by hand instead of
through the meter's own ``workspace_id`` -> owner rule. Passing
``workspace_id`` here is what makes a member's scrape inside someone else's
workspace spend that workspace owner's wallet, same as every other metered
action (``custom/WORKSPACE_CREDITS.md``).

The per-action on/off flags (``WEB_CRAWL_CREDIT_BILLING_ENABLED``,
``WEB_CRAWL_CAPTCHA_BILLING_ENABLED``, ``PLATFORM_SCRAPE_BILLING_ENABLED``,
all off by default for self-hosted/OSS installs) are unchanged and still
gate before any meter call -- the billing *mode* (off/shadow/live) is a
separate knob, and a deployment that never turned crawl billing on must stay
exactly as free as it was.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

from sqlalchemy import select

from app.billing import meter
from app.billing.errors import BillingDenied
from app.capabilities.core.types import (
    BillableInput,
    BillableOutput,
    BillingUnit,
    CapabilityContext,
)
from app.config import config
from app.services.etl_credit_service import InsufficientCreditsError
from app.services.token_tracking_service import record_token_usage
from app.utils.captcha import captcha_enabled

if TYPE_CHECKING:
    from uuid import UUID

    from sqlalchemy.ext.asyncio import AsyncSession

__all__ = ["charge_capability", "gate_capability", "pricing_meters"]

# BillingUnit.value *is* the billing_action_prices action name (c0021) and
# the token_usage.usage_type string record_token_usage already wrote --
# one name for the same meter across all three.
_WEB_CRAWL_ACTION = "web_crawl"
_WEB_CRAWL_CAPTCHA_ACTION = "web_crawl_captcha"

# Display noun for each platform meter, e.g. "$3.50 / 1k places".
_UNIT_NOUNS: dict[BillingUnit, str] = {
    BillingUnit.REDDIT_ITEM: "item",
    BillingUnit.GOOGLE_SEARCH_SERP: "SERP",
    BillingUnit.GOOGLE_MAPS_PLACE: "place",
    BillingUnit.GOOGLE_MAPS_REVIEW: "review",
    BillingUnit.AMAZON_PRODUCT: "product",
    BillingUnit.YOUTUBE_VIDEO: "video",
    BillingUnit.YOUTUBE_COMMENT: "comment",
    BillingUnit.INSTAGRAM_ITEM: "item",
    BillingUnit.INSTAGRAM_COMMENT: "comment",
    BillingUnit.TIKTOK_VIDEO: "video",
    BillingUnit.TIKTOK_USER: "profile",
    BillingUnit.TIKTOK_COMMENT: "comment",
    BillingUnit.INDEED_JOB: "job",
    BillingUnit.WALMART_PRODUCT: "product",
    BillingUnit.WALMART_REVIEW: "review",
    BillingUnit.EXA_SEARCH: "search",
    BillingUnit.TAVILY_SEARCH: "search",
}


async def pricing_meters(session: AsyncSession, unit: BillingUnit | None) -> list[dict]:
    """The live per-item rates a verb charges, for UI display. Empty = free.

    Mirrors the gate/charge logic exactly: meters whose billing flag is off
    are omitted, so a self-hosted install with billing disabled reads as
    free. Prices come from ``billing_action_prices`` -- the same row
    ``gate_capability``/``charge_capability`` price against -- so this never
    drifts from what a run is actually charged once an admin edits a price.
    """
    if unit is None:
        return []
    if unit is BillingUnit.WEB_CRAWL:
        meters = []
        if config.WEB_CRAWL_CREDIT_BILLING_ENABLED:
            meters.append(
                {
                    "unit": "page",
                    "micros_per_unit": await meter.action_price(
                        session, _WEB_CRAWL_ACTION
                    ),
                }
            )
        if config.WEB_CRAWL_CAPTCHA_BILLING_ENABLED and captcha_enabled():
            meters.append(
                {
                    "unit": "captcha solve",
                    "micros_per_unit": await meter.action_price(
                        session, _WEB_CRAWL_CAPTCHA_ACTION
                    ),
                }
            )
        return meters
    if not config.PLATFORM_SCRAPE_BILLING_ENABLED:
        return []
    meters = [
        {
            "unit": _UNIT_NOUNS[unit],
            "micros_per_unit": await meter.action_price(session, unit.value),
        }
    ]
    if unit is BillingUnit.GOOGLE_MAPS_PLACE:
        # Dual-metered: attached reviews bill on their own meter.
        review = BillingUnit.GOOGLE_MAPS_REVIEW
        meters.append(
            {
                "unit": _UNIT_NOUNS[review],
                "micros_per_unit": await meter.action_price(session, review.value),
            }
        )
    return meters


def _as_insufficient_credits(exc: BillingDenied) -> InsufficientCreditsError:
    """Translate the meter's refusal into the exception the capability doors
    already catch (``app/capabilities/core/access/agent.py`` and ``rest.py``),
    so routing through the meter needs no change there."""
    return InsufficientCreditsError(
        message=exc.message,
        balance_micros=max(0, exc.available_micros),
        required_micros=exc.required_micros,
        code=exc.code,
    )


async def gate_capability(
    payload: BillableInput, unit: BillingUnit | None, ctx: CapabilityContext
) -> None:
    """Pre-flight: block an over-budget owner before the executor runs (03c).

    Raises ``InsufficientCreditsError`` when the wallet can't cover the
    input's worst-case ``estimated_units``. ``None`` unit = free = no gate.
    """
    if unit is None:
        return
    if unit is BillingUnit.WEB_CRAWL:
        await _gate_web_crawl(ctx, payload.estimated_units)
        return
    await _gate_platform(payload, unit, ctx)


async def _gate_web_crawl(ctx: CapabilityContext, estimated_successes: int) -> None:
    """Check the worst-case cost: crawl successes + worst-case captcha attempts.

    Captcha budget is only checked when solving is actually enabled -- with
    solving off, attempts can never happen, so checking for it would wrongly
    block a run for captcha that will never be attempted. Mirrors the
    indexer path (3d).
    """
    crawl_on = config.WEB_CRAWL_CREDIT_BILLING_ENABLED
    captcha_on = config.WEB_CRAWL_CAPTCHA_BILLING_ENABLED and captcha_enabled()
    if not crawl_on and not captcha_on:
        return
    owner_user_id = await _resolve_workspace_owner(ctx.session, ctx.workspace_id)
    if owner_user_id is None:
        return

    if crawl_on and estimated_successes > 0:
        await _check(owner_user_id, _WEB_CRAWL_ACTION, estimated_successes, ctx)
    if captcha_on:
        worst_case_attempts = estimated_successes * config.CAPTCHA_MAX_ATTEMPTS_PER_URL
        if worst_case_attempts > 0:
            await _check(
                owner_user_id,
                _WEB_CRAWL_CAPTCHA_ACTION,
                worst_case_attempts,
                ctx,
            )


async def _gate_platform(
    payload: BillableInput, unit: BillingUnit, ctx: CapabilityContext
) -> None:
    """Check the worst-case per-item cost for a platform scraper verb.

    ``google_maps.scrape`` is dual-metered: it can attach reviews per place,
    so its gate also checks ``estimated_review_units`` on the review meter --
    same two-meters-one-verb shape as crawl + captcha.
    """
    if not config.PLATFORM_SCRAPE_BILLING_ENABLED:
        return
    owner_user_id = await _resolve_workspace_owner(ctx.session, ctx.workspace_id)
    if owner_user_id is None:
        return

    if payload.estimated_units > 0:
        await _check(owner_user_id, unit.value, payload.estimated_units, ctx)
    if unit is BillingUnit.GOOGLE_MAPS_PLACE:
        review_units = getattr(payload, "estimated_review_units", 0)
        if review_units > 0:
            await _check(
                owner_user_id, BillingUnit.GOOGLE_MAPS_REVIEW.value, review_units, ctx
            )


async def _check(owner_user_id: UUID, action: str, units: int, ctx: CapabilityContext) -> None:
    try:
        await meter.check(
            owner_user_id,
            action,
            units=units,
            workspace_id=ctx.workspace_id,
            session_factory=meter.sibling_sessions(ctx.session),
        )
    except BillingDenied as exc:
        raise _as_insufficient_credits(exc) from exc


async def charge_capability(
    output: BillableOutput, unit: BillingUnit | None, ctx: CapabilityContext
) -> int:
    """Bill the workspace owner for this result and return the micros charged.

    For crawl-backed verbs this also bills any captcha *attempts* (Phase 3d)
    as a separate per-attempt unit -- the solver charges per attempt even
    when the crawl ultimately failed, so it can't ride the per-success crawl
    meter. Platform verbs bill per item returned; ``google_maps.scrape``
    additionally bills its attached reviews. ``None`` unit = free = returns 0.

    Charging already-done work never refuses (the gate already ran); a
    short balance lets the top-up bucket dip below zero, same as before.

    The returned total lets the doors persist a per-run ``cost_micros``.
    """
    if unit is None:
        return 0
    if unit is BillingUnit.WEB_CRAWL:
        charged = await _charge_web_crawl(ctx, output.billable_units)
        charged += await _charge_captcha(ctx, getattr(output, "captcha_attempts", 0))
        return charged
    return await _charge_platform(output, unit, ctx)


async def _charge_web_crawl(ctx: CapabilityContext, successes: int) -> int:
    if successes <= 0 or not config.WEB_CRAWL_CREDIT_BILLING_ENABLED:
        return 0
    owner_user_id = await _resolve_workspace_owner(ctx.session, ctx.workspace_id)
    if owner_user_id is None:
        return 0
    return await _record(
        owner_user_id,
        _WEB_CRAWL_ACTION,
        successes,
        ctx,
        usage_type="web_crawl",
        call_details={"successes": successes},
    )


async def _charge_captcha(ctx: CapabilityContext, attempts: int) -> int:
    if attempts <= 0 or not config.WEB_CRAWL_CAPTCHA_BILLING_ENABLED:
        return 0
    owner_user_id = await _resolve_workspace_owner(ctx.session, ctx.workspace_id)
    if owner_user_id is None:
        return 0
    return await _record(
        owner_user_id,
        _WEB_CRAWL_CAPTCHA_ACTION,
        attempts,
        ctx,
        usage_type="web_crawl_captcha",
        call_details={"attempts": attempts},
    )


async def _charge_platform(
    output: BillableOutput, unit: BillingUnit, ctx: CapabilityContext
) -> int:
    """Charge a platform verb per item; dual-meter ``google_maps.scrape`` reviews."""
    if not config.PLATFORM_SCRAPE_BILLING_ENABLED:
        return 0
    owner_user_id = await _resolve_workspace_owner(ctx.session, ctx.workspace_id)
    if owner_user_id is None:
        return 0

    charged = await _charge_platform_meter(
        owner_user_id, ctx, unit, output.billable_units
    )
    if unit is BillingUnit.GOOGLE_MAPS_PLACE:
        reviews = getattr(output, "attached_review_count", 0)
        charged += await _charge_platform_meter(
            owner_user_id, ctx, BillingUnit.GOOGLE_MAPS_REVIEW, reviews
        )
    return charged


async def _charge_platform_meter(
    owner_user_id: UUID,
    ctx: CapabilityContext,
    unit: BillingUnit,
    items: int,
) -> int:
    if items <= 0:
        return 0
    return await _record(
        owner_user_id,
        unit.value,
        items,
        ctx,
        usage_type=unit.value,
        call_details={"items": items},
    )


async def _record(
    owner_user_id: UUID,
    action: str,
    units: int,
    ctx: CapabilityContext,
    *,
    usage_type: str,
    call_details: dict,
) -> int:
    charged_micros = await meter.record(
        owner_user_id,
        action,
        units=units,
        workspace_id=ctx.workspace_id,
        session_factory=meter.sibling_sessions(ctx.session),
    )
    # The cost/call audit trail (token_usage), independent of billing_usage
    # (the meter's own ledger row) -- unchanged from before c0021.
    await record_token_usage(
        ctx.session,
        usage_type=usage_type,
        workspace_id=ctx.workspace_id,
        user_id=owner_user_id,
        cost_micros=charged_micros,
        call_details=call_details,
    )
    return charged_micros


async def _resolve_workspace_owner(
    session: AsyncSession, workspace_id: int
) -> UUID | None:
    """The ``user_id`` that owns ``workspace_id`` (the crawl payer, not the
    caller). ``meter.check``/``meter.record`` re-resolve this themselves from
    ``workspace_id``; it is needed here too only because they require a
    non-optional ``user_id`` and there is no separate acting-user on
    ``CapabilityContext`` to pass instead -- and to skip billing entirely
    (rather than silently falling back to some other user) when the
    workspace is gone."""
    from app.db import Workspace

    result = await session.execute(
        select(Workspace.user_id).where(Workspace.id == workspace_id)
    )
    return result.scalar_one_or_none()
