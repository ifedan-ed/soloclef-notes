"""The chat's Exa, Tavily and Browserbase tools on the real meter (c0022): the
workspace owner's wallet pays, at the seeded prices.

The charge is made by ``workspace_id`` alone: the agent's capability tools
are built per workspace and do not know which member asked, so whoever
chatted, the owner pays and the usage row records the owner as ``user_id``
too (as for every capability charge, Google Search included)."""

from __future__ import annotations

import pytest
from sqlalchemy import select

from app.billing import ledger
from app.billing.persistence.models import BillingUsage
from app.billing.units import credits_to_micros
from app.capabilities.browserbase.read_page.schemas import PageText, ReadPageOutput
from app.capabilities.core.billing import charge_capability, gate_capability
from app.capabilities.core.types import BillingUnit, CapabilityContext
from app.capabilities.web_search_api.schemas import WebSearchInput, WebSearchOutput
from app.config import config
from app.services.etl_credit_service import InsufficientCreditsError

pytestmark = pytest.mark.integration


def _ctx(session, workspace_id: int) -> CapabilityContext:
    return CapabilityContext(session=session, workspace_id=workspace_id)


async def _spendable(db_session, user_id) -> int:
    account, user = await ledger.lock_account(db_session, user_id)
    return ledger.balances(account, user).spendable_micros


async def _usage(db_session, action: str) -> list[BillingUsage]:
    return list(
        (
            await db_session.scalars(
                select(BillingUsage).where(BillingUsage.action == action)
            )
        ).all()
    )


@pytest.mark.parametrize(
    ("unit", "action", "credits"),
    [
        (BillingUnit.EXA_SEARCH, "exa_search", 1.5),
        (BillingUnit.TAVILY_SEARCH, "tavily_search", 0.8),
    ],
)
async def test_a_search_in_a_workspace_spends_its_owners_wallet(
    db_session,
    db_user,
    db_workspace,
    set_mode,
    monkeypatch,
    unit,
    action,
    credits,
):
    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    await set_mode("live")
    before = await _spendable(db_session, db_user.id)  # db_user owns db_workspace

    charged = await charge_capability(
        WebSearchOutput(provider="exa", query="q"),
        unit,
        _ctx(db_session, db_workspace.id),
    )

    assert charged == credits_to_micros(credits)
    assert await _spendable(db_session, db_user.id) == before - charged
    [usage] = await _usage(db_session, action)
    assert usage.units == 1
    assert usage.payer_user_id == db_user.id
    # No acting member reaches the charge: the owner is recorded as the doer.
    assert usage.user_id == db_user.id


async def test_a_browserbase_page_is_one_web_page_read(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "WEB_CRAWL_CREDIT_BILLING_ENABLED", True)
    await set_mode("live")

    charged = await charge_capability(
        ReadPageOutput(items=[PageText(url="https://a.example", markdown="text")]),
        BillingUnit.WEB_CRAWL,
        _ctx(db_session, db_workspace.id),
    )

    assert charged == credits_to_micros(0.2)
    [usage] = await _usage(db_session, "web_crawl")
    assert usage.units == 1 and usage.payer_user_id == db_user.id


async def test_a_search_is_refused_before_it_runs_when_the_wallet_is_short(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    await set_mode("live")
    account, user = await ledger.lock_account(db_session, db_user.id)
    account.plan_credits_micros = 0
    user.credit_micros_balance = 0
    await db_session.flush()

    with pytest.raises(InsufficientCreditsError):
        await gate_capability(
            WebSearchInput(query="q"),
            BillingUnit.EXA_SEARCH,
            _ctx(db_session, db_workspace.id),
        )
