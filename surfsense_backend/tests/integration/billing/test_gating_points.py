"""Each gate: chat model tier, parser choice, page charges, vision, uploads."""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest
from sqlalchemy import select

from app.billing import ledger
from app.billing.model_tiers import set_tier
from app.billing.parsers import parser_within_plan
from app.billing.persistence.models import BillingPlan, BillingUsage
from app.billing.units import credits_to_micros
from app.etl_pipeline.etl_document import DocumentParser
from app.services.billable_calls import QuotaInsufficientError
from app.services.etl_credit_service import EtlCreditService, InsufficientCreditsError
from app.services.quota_checked_vision_llm import QuotaCheckedVisionLLM
from app.tasks.chat.streaming.flows.shared.premium_quota import (
    finalize_credit,
    release_credit,
    reserve_credit,
)

pytestmark = pytest.mark.integration


def _agent(model="gpt-big", tier="free", config_id=-5):
    return SimpleNamespace(config_id=config_id, model_name=model, billing_tier=tier)


async def _upgrade(db_session, user, slug="plus"):
    plan = await db_session.scalar(select(BillingPlan).where(BillingPlan.slug == slug))
    account, u = await ledger.lock_account(db_session, user.id)
    await ledger.change_plan(db_session, account, u, plan, source="admin", reason="t")
    await db_session.flush()


async def test_free_plan_chat_on_a_premium_model_is_refused(
    db_session, db_user, set_mode
):
    await set_mode("live")
    reservation = await reserve_credit(
        agent_config=_agent(tier="premium"), user_id=str(db_user.id)
    )
    assert not reservation.allowed
    assert reservation.error_code == "PLAN_MODEL_TIER_NOT_INCLUDED"


async def test_admin_tier_overrides_the_model_config(db_session, db_user, set_mode):
    await set_mode("live")
    await set_tier(db_session, "gpt-big", "premium")
    reservation = await reserve_credit(
        agent_config=_agent(tier="free"), user_id=str(db_user.id)
    )
    assert reservation.error_code == "PLAN_MODEL_TIER_NOT_INCLUDED"


async def test_a_chat_turn_costs_its_tier_price(db_session, db_user, set_mode):
    await set_mode("live")
    await _upgrade(db_session, db_user)
    reservation = await reserve_credit(
        agent_config=_agent(tier="premium"), user_id=str(db_user.id)
    )
    assert reservation.allowed
    acc = SimpleNamespace(total_cost_micros=1234, grand_total=99)
    await finalize_credit(
        reservation=reservation, user_id=str(db_user.id), accumulator=acc
    )
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_credits_micros == credits_to_micros(1500 - 5)
    usage = await db_session.get(BillingUsage, int(reservation.request_id))
    assert usage.details["provider_cost_micros"] == 1234

    standard = await reserve_credit(
        agent_config=_agent(tier="free"), user_id=str(db_user.id)
    )
    await release_credit(
        reservation=standard, user_id=str(db_user.id)
    )  # cancelled turn
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_credits_micros == credits_to_micros(1495)
    assert account.reserved_micros == 0


async def test_cloud_parsing_is_refused_on_free_and_local_is_free(
    db_session, db_user, set_mode
):
    await set_mode("live")
    service = EtlCreditService(db_session)
    with pytest.raises(InsufficientCreditsError) as denied:
        await service.check_credits(str(db_user.id), 5, parser=DocumentParser.CLOUD)
    assert denied.value.code == "PLAN_PARSER_NOT_INCLUDED"
    await service.check_credits(str(db_user.id), 5, parser=DocumentParser.LOCAL)
    with pytest.raises(InsufficientCreditsError) as too_big:
        await service.check_credits(str(db_user.id), 51, parser=DocumentParser.LOCAL)
    assert too_big.value.code == "PLAN_TOO_MANY_PAGES"


async def test_cloud_pages_are_charged_after_parsing(db_session, db_user, set_mode):
    await set_mode("live")
    await _upgrade(db_session, db_user)
    # Unique, so an upload test that already billed document 1 cannot collide.
    reference = f"document_pages:gating-{uuid4().hex}"
    charged = await EtlCreditService(db_session).charge_credits(
        str(db_user.id), 7, parser=DocumentParser.CLOUD, reference=reference
    )
    assert charged == credits_to_micros(14)
    again = await EtlCreditService(db_session).charge_credits(
        str(db_user.id), 7, parser=DocumentParser.CLOUD, reference=reference
    )
    assert again == 0  # the same document is not charged twice


async def test_research_imports_parse_locally_on_free(db_session, db_user, set_mode):
    await set_mode("live")
    assert (
        await parser_within_plan(db_user.id, DocumentParser.CLOUD)
        == DocumentParser.LOCAL
    )
    await set_mode("shadow")
    assert (
        await parser_within_plan(db_user.id, DocumentParser.CLOUD)
        == DocumentParser.CLOUD
    )


class _FakeVision:
    def __init__(self):
        self.calls = 0

    async def ainvoke(self, _input, *a, **k):
        self.calls += 1
        return "a chart of revenue"


async def test_vision_is_a_plan_feature_and_costs_per_image(
    db_session, db_user, db_workspace, set_mode, monkeypatch
):
    import app.services.billable_calls as bc
    from app.billing.meter import sibling_sessions

    monkeypatch.setattr(bc, "shielded_async_session", sibling_sessions(db_session))
    await set_mode("live")
    inner = _FakeVision()
    llm = QuotaCheckedVisionLLM(inner, user_id=db_user.id, workspace_id=db_workspace.id)
    with pytest.raises(QuotaInsufficientError) as denied:
        await llm.ainvoke("describe")
    assert denied.value.code == "PLAN_VISION_NOT_INCLUDED"
    assert inner.calls == 0
    await _upgrade(db_session, db_user)
    assert await llm.ainvoke("describe") == "a chart of revenue"
    account, _ = await ledger.lock_account(db_session, db_user.id)
    assert account.plan_credits_micros == credits_to_micros(1499)


async def test_upload_route_refuses_cloud_parsing_on_free(
    db_session, db_user, db_workspace, set_mode, api
):
    await set_mode("live")
    async with api(db_user) as client:
        response = await client.post(
            "/api/v1/documents/fileupload",
            data={"workspace_id": str(db_workspace.id), "parser": "cloud"},
            files={"files": ("a.pdf", b"%PDF-1.4 test", "application/pdf")},
        )
    if response.status_code == 400 and "unavailable" in response.text:
        pytest.skip("cloud parser not configured in this environment")
    assert response.status_code == 402
    assert response.json()["error"]["code"] == "PLAN_PARSER_NOT_INCLUDED"
