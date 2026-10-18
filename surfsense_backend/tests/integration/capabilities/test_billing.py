"""Scrape/crawl billing on the real meter (c0021): the workspace owner's
wallet, ``billing_usage`` rows, and real plan/balance enforcement --
replacing the old ``wallet_credit.py`` direct-debit path this module used to
exercise with mocked sessions.
"""

from __future__ import annotations

import pytest
from sqlalchemy import select

from app.billing import ledger
from app.billing.persistence.models import BillingUsage
from app.billing.units import credits_to_micros
from app.capabilities.core.billing import charge_capability, gate_capability
from app.capabilities.core.types import BillingUnit, CapabilityContext
from app.capabilities.web.crawl.schemas import CrawlInput, CrawlItem, CrawlOutput
from app.config import config
from app.services.etl_credit_service import InsufficientCreditsError

pytestmark = pytest.mark.integration


def _ctx(session, workspace_id: int) -> CapabilityContext:
    return CapabilityContext(session=session, workspace_id=workspace_id)


def _output(*statuses: str) -> CrawlOutput:
    return CrawlOutput(
        items=[
            CrawlItem(url=f"https://{i}.com", status=status)
            for i, status in enumerate(statuses)
        ]
    )


class _PlatformOutput:
    def __init__(self, items: int, attached_review_count: int = 0):
        self.billable_units = items
        self.attached_review_count = attached_review_count


class _PlatformInput:
    def __init__(self, estimated_units: int, estimated_review_units: int = 0):
        self.estimated_units = estimated_units
        self.estimated_review_units = estimated_review_units


async def _spendable(db_session, user_id) -> int:
    """Plan credits + top-up, combined -- a debit may come out of either
    bucket (plan credits spend first), so tests pin the total, not one
    column."""
    account, user = await ledger.lock_account(db_session, user_id)
    return ledger.balances(account, user).spendable_micros


async def _broke(db_session, user_id, *, leftover_micros: int = 0) -> None:
    """Zero out both buckets (plan credits spend first -- zeroing only the
    top-up balance leaves the free plan's own monthly allowance to spend
    from, which would let a short-top-up test pass for the wrong reason)."""
    account, user = await ledger.lock_account(db_session, user_id)
    account.plan_credits_micros = 0
    user.credit_micros_balance = leftover_micros
    await db_session.flush()


async def _usage(db_session, action: str) -> list[BillingUsage]:
    return list(
        (
            await db_session.scalars(
                select(BillingUsage).where(BillingUsage.action == action)
            )
        ).all()
    )


# ---------------------------------------------------------------------------
# Web crawl
# ---------------------------------------------------------------------------


async def test_web_crawl_charges_the_workspace_owner_per_success(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "WEB_CRAWL_CREDIT_BILLING_ENABLED", True)
    await set_mode("live")
    before = await _spendable(db_session, db_user.id)

    charged = await charge_capability(
        _output("success", "empty", "success"),
        BillingUnit.WEB_CRAWL,
        _ctx(db_session, db_workspace.id),
    )

    assert charged == 2 * credits_to_micros(0.2)
    assert await _spendable(db_session, db_user.id) == before - charged
    [usage] = await _usage(db_session, "web_crawl")
    assert usage.payer_user_id == db_user.id
    assert usage.workspace_id == db_workspace.id
    assert usage.units == 2
    assert usage.charged_micros == charged


async def test_web_crawl_disabled_flag_is_still_a_noop(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    """The per-feature flag (off by default) still gates before the meter,
    independent of the billing mode -- a deployment that never turned crawl
    billing on stays exactly as free as it was."""
    monkeypatch.setattr(config, "WEB_CRAWL_CREDIT_BILLING_ENABLED", False)
    await set_mode("live")
    before = db_user.credit_micros_balance

    charged = await charge_capability(
        _output("success", "success"),
        BillingUnit.WEB_CRAWL,
        _ctx(db_session, db_workspace.id),
    )

    assert charged == 0
    assert await _usage(db_session, "web_crawl") == []
    await db_session.refresh(db_user)
    assert db_user.credit_micros_balance == before


async def test_web_crawl_captcha_bills_attempts_even_when_the_crawl_failed(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "WEB_CRAWL_CAPTCHA_BILLING_ENABLED", True)
    monkeypatch.setattr("app.capabilities.core.billing.captcha_enabled", lambda: True)
    await set_mode("live")
    db_user.credit_micros_balance = credits_to_micros(100)
    await db_session.flush()

    output = _output("failed")
    output.captcha_attempts = 2

    charged = await charge_capability(
        output, BillingUnit.WEB_CRAWL, _ctx(db_session, db_workspace.id)
    )

    assert charged == 2 * credits_to_micros(0.3)
    [usage] = await _usage(db_session, "web_crawl_captcha")
    assert usage.units == 2


async def test_web_crawl_gate_refuses_in_live_mode_when_the_wallet_is_short(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    """This is the bug fix: previously ``check_balance`` was a hard no-op and
    a crawl was never refused for want of credit. Routed through the real
    meter, live mode now enforces it like every other billable action."""
    monkeypatch.setattr(config, "WEB_CRAWL_CREDIT_BILLING_ENABLED", True)
    await set_mode("live")
    await _broke(db_session, db_user.id, leftover_micros=credits_to_micros(0.1))

    with pytest.raises(InsufficientCreditsError):
        await gate_capability(
            CrawlInput(startUrls=["https://a.com"]),
            BillingUnit.WEB_CRAWL,
            _ctx(db_session, db_workspace.id),
        )


async def test_web_crawl_gate_passes_when_the_wallet_covers_it(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "WEB_CRAWL_CREDIT_BILLING_ENABLED", True)
    await set_mode("live")
    db_user.credit_micros_balance = credits_to_micros(100)
    await db_session.flush()

    await gate_capability(
        CrawlInput(startUrls=["https://a.com", "https://b.com"]),
        BillingUnit.WEB_CRAWL,
        _ctx(db_session, db_workspace.id),
    )  # does not raise


async def test_web_crawl_gate_in_shadow_mode_never_refuses(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "WEB_CRAWL_CREDIT_BILLING_ENABLED", True)
    await set_mode("shadow")
    await _broke(db_session, db_user.id)

    await gate_capability(
        CrawlInput(startUrls=["https://a.com"]),
        BillingUnit.WEB_CRAWL,
        _ctx(db_session, db_workspace.id),
    )  # does not raise


async def test_free_verb_without_a_unit_is_a_noop(db_session, db_user, db_workspace):
    charged = await charge_capability(
        _output("success"), None, _ctx(db_session, db_workspace.id)
    )
    assert charged == 0
    await gate_capability(
        CrawlInput(startUrls=["https://a.com"]), None, _ctx(db_session, db_workspace.id)
    )  # does not raise


# ---------------------------------------------------------------------------
# Platform scrape
# ---------------------------------------------------------------------------


async def test_platform_charges_owner_per_item(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    await set_mode("live")
    before = await _spendable(db_session, db_user.id)

    charged = await charge_capability(
        _PlatformOutput(3), BillingUnit.REDDIT_ITEM, _ctx(db_session, db_workspace.id)
    )

    assert charged == 3 * credits_to_micros(0.35)
    assert await _spendable(db_session, db_user.id) == before - charged
    [usage] = await _usage(db_session, "reddit_item")
    assert usage.units == 3
    assert usage.payer_user_id == db_user.id


async def test_platform_maps_scrape_dual_meters_places_and_reviews(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    await set_mode("live")

    charged = await charge_capability(
        _PlatformOutput(2, attached_review_count=10),
        BillingUnit.GOOGLE_MAPS_PLACE,
        _ctx(db_session, db_workspace.id),
    )

    expected = 2 * credits_to_micros(0.35) + 10 * credits_to_micros(0.15)
    assert charged == expected
    assert len(await _usage(db_session, "google_maps_place")) == 1
    assert len(await _usage(db_session, "google_maps_review")) == 1


async def test_platform_charge_disabled_is_a_noop(
    db_session, db_user, db_workspace, monkeypatch
):
    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", False)
    charged = await charge_capability(
        _PlatformOutput(3), BillingUnit.REDDIT_ITEM, _ctx(db_session, db_workspace.id)
    )
    assert charged == 0
    assert await _usage(db_session, "reddit_item") == []


async def test_platform_gate_refuses_in_live_mode_when_the_wallet_is_short(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    await set_mode("live")
    await _broke(db_session, db_user.id)

    with pytest.raises(InsufficientCreditsError):
        await gate_capability(
            _PlatformInput(estimated_units=2),
            BillingUnit.GOOGLE_SEARCH_SERP,
            _ctx(db_session, db_workspace.id),
        )


# ---------------------------------------------------------------------------
# Workspace-owner payer: a member's scrape spends the owner's wallet
# ---------------------------------------------------------------------------


async def test_a_members_scrape_spends_the_workspace_owners_wallet(
    db_session, db_user, db_workspace, make_user, add_member, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    await set_mode("live")
    before = await _spendable(db_session, db_user.id)  # db_user owns db_workspace
    member = await make_user()
    await add_member(db_workspace, member)

    # The capability layer has no "acting user" of its own (it bills whoever
    # owns workspace_id); this pins that the owner -- not the member -- pays,
    # the same rule every other metered action follows (c0019).
    charged = await charge_capability(
        _PlatformOutput(1), BillingUnit.YOUTUBE_VIDEO, _ctx(db_session, db_workspace.id)
    )

    assert charged == credits_to_micros(0.25)
    assert await _spendable(db_session, db_user.id) == before - charged
    [usage] = await _usage(db_session, "youtube_video")
    assert usage.payer_user_id == db_user.id
