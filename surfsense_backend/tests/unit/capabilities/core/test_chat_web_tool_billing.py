"""The chat's Exa, Tavily and Browserbase tools go through the same meter as
every other paid web tool: checked and charged to the workspace (whose owner
pays, ``meter._resolve_payer``), one search or one page at a time."""

from __future__ import annotations

import importlib.util
import uuid
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from app.billing.defaults import ACTION_PRICES
from app.billing.units import credits_to_micros
from app.capabilities.browserbase.read_page.schemas import (
    PageText,
    ReadPageInput,
    ReadPageOutput,
)
from app.capabilities.core import billing
from app.capabilities.core.types import BillingUnit, CapabilityContext
from app.capabilities.web_search_api.schemas import WebSearchInput, WebSearchOutput

pytestmark = pytest.mark.unit

OWNER = uuid.uuid4()
WORKSPACE = 7


@pytest.fixture
def meter(monkeypatch):
    from app.config import config

    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", True)
    monkeypatch.setattr(config, "WEB_CRAWL_CREDIT_BILLING_ENABLED", True)
    monkeypatch.setattr(config, "WEB_CRAWL_CAPTCHA_BILLING_ENABLED", False)
    monkeypatch.setattr(
        billing, "_resolve_workspace_owner", AsyncMock(return_value=OWNER)
    )
    record = AsyncMock(return_value=15_000)
    check = AsyncMock()
    monkeypatch.setattr(billing.meter, "record", record)
    monkeypatch.setattr(billing.meter, "check", check)
    monkeypatch.setattr(billing, "record_token_usage", AsyncMock())
    return SimpleNamespace(record=record, check=check)


def _ctx() -> CapabilityContext:
    return CapabilityContext(session=SimpleNamespace(), workspace_id=WORKSPACE)


@pytest.mark.parametrize(
    ("unit", "action"),
    [
        (BillingUnit.EXA_SEARCH, "exa_search"),
        (BillingUnit.TAVILY_SEARCH, "tavily_search"),
    ],
)
async def test_a_search_is_checked_then_charged_once_to_the_workspace(
    meter, unit, action
):
    await billing.gate_capability(WebSearchInput(query="q"), unit, _ctx())
    charged = await billing.charge_capability(
        WebSearchOutput(provider="exa", query="q"), unit, _ctx()
    )

    check = meter.check.await_args
    assert check.args == (OWNER, action)
    assert check.kwargs["units"] == 1 and check.kwargs["workspace_id"] == WORKSPACE
    record = meter.record.await_args
    assert record.args == (OWNER, action)
    assert record.kwargs["units"] == 1 and record.kwargs["workspace_id"] == WORKSPACE
    assert charged == 15_000


async def test_a_browserbase_page_is_one_web_page_read(meter):
    output = ReadPageOutput(items=[PageText(url="https://a.example", markdown="x")])

    await billing.gate_capability(
        ReadPageInput(url="https://a.example"), BillingUnit.WEB_CRAWL, _ctx()
    )
    await billing.charge_capability(output, BillingUnit.WEB_CRAWL, _ctx())

    assert meter.check.await_args.args == (OWNER, "web_crawl")
    record = meter.record.await_args
    assert record.args == (OWNER, "web_crawl")
    assert record.kwargs["units"] == 1 and record.kwargs["workspace_id"] == WORKSPACE


async def test_searches_are_free_where_platform_billing_is_off(meter, monkeypatch):
    from app.config import config

    monkeypatch.setattr(config, "PLATFORM_SCRAPE_BILLING_ENABLED", False)

    charged = await billing.charge_capability(
        WebSearchOutput(provider="tavily", query="q"),
        BillingUnit.TAVILY_SEARCH,
        _ctx(),
    )

    assert charged == 0
    meter.record.assert_not_awaited()


def test_the_migration_seeds_the_default_prices():
    path = (
        Path(__file__).resolve().parents[4]
        / "alembic"
        / "versions"
        / "c0022_chat_web_search_prices.py"
    )
    spec = importlib.util.spec_from_file_location("c0022", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    seeded = {action: (credits, unit) for action, credits, unit, _ in module._PRICES}
    assert seeded == {
        action: ACTION_PRICES[action][:2] for action in ("exa_search", "tavily_search")
    }
    assert credits_to_micros(seeded["exa_search"][0]) == 15_000
    assert credits_to_micros(seeded["tavily_search"][0]) == 8_000
