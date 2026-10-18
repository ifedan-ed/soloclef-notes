"""The billing API: own data only, admin-only administration, write-only secrets."""

from __future__ import annotations

import pytest
from sqlalchemy import select

from app.billing import meter
from app.billing.persistence.models import BillingPlan

pytestmark = pytest.mark.integration


async def test_me_shows_plan_balances_and_prices(db_user, api, set_mode):
    await set_mode("shadow")
    async with api(db_user) as client:
        body = (await client.get("/api/v1/billing/me")).json()
    assert body["mode"] == "shadow"
    assert body["plan"]["slug"] == "free"
    assert body["balances"]["plan_credits"] == 100
    assert {p["action"]: p["credits"] for p in body["prices"]}["studio_report"] == 20
    assert [p["slug"] for p in body["plans"]] == ["free", "plus", "pro"]
    assert body["is_admin"] is False


async def test_the_catalog_is_public(api):
    async with api(None) as client:
        response = await client.get("/api/v1/billing/catalog")
    assert response.status_code == 200 and len(response.json()["plans"]) == 3


async def test_usage_history_is_only_your_own(db_user, make_user, api, set_mode):
    await set_mode("shadow")
    other = await make_user()
    await meter.settle(await meter.begin(other.id, "studio_report", notebook_id=4))
    await meter.settle(await meter.begin(db_user.id, "studio_quiz", notebook_id=3))
    async with api(db_user) as client:
        body = (await client.get("/api/v1/billing/usage")).json()
        filtered = (
            await client.get("/api/v1/billing/usage", params={"notebook_id": 4})
        ).json()
    assert [i["action"] for i in body["items"]] == ["studio_quiz"]
    assert body["by_action"] == [{"action": "studio_quiz", "count": 1, "credits": 5}]
    assert filtered["items"] == []


async def test_admin_routes_are_refused_to_everyone_else(db_user, api):
    async with api(db_user) as client:
        for method, path in [
            ("get", "/api/v1/billing/admin/overview"),
            ("put", "/api/v1/billing/admin/settings"),
            ("get", "/api/v1/billing/admin/users"),
            ("post", f"/api/v1/billing/admin/users/{db_user.id}/adjust"),
            ("get", "/api/v1/billing/admin/report"),
        ]:
            response = await getattr(client, method)(
                path, **({"json": {}} if method != "get" else {})
            )
            assert response.status_code == 403, path


async def test_admin_edits_prices_plans_and_credits_and_secrets_stay_hidden(
    db_session, db_user, new_user, api
):
    admin = await new_user("owner@surfsense.net")
    async with api(admin) as client:
        saved = await client.put(
            "/api/v1/billing/admin/settings",
            json={
                "values": {"mode": "live"},
                "secrets": {"stripe_secret_key": "sk_test_SECRETVALUE"},
            },
        )
        assert saved.status_code == 200
        assert "sk_test_SECRETVALUE" not in saved.text
        assert saved.json()["secrets"]["stripe_secret_key"]["set"] is True
        assert saved.json()["stripe_mode"] == "test"
        overview = await client.get("/api/v1/billing/admin/overview")
        assert "sk_test_SECRETVALUE" not in overview.text

        assert (
            await client.put(
                "/api/v1/billing/admin/prices/studio_report", json={"credits": 25}
            )
        ).json()["credits"] == 25
        bad_mode = await client.put(
            "/api/v1/billing/admin/settings", json={"values": {"mode": "free-for-all"}}
        )
        assert bad_mode.status_code == 400

        adjusted = await client.post(
            f"/api/v1/billing/admin/users/{db_user.id}/adjust",
            json={"credits": 42, "reason": "Goodwill"},
        )
        assert adjusted.json()["topup_credits"] == 42
        pro = await db_session.scalar(
            select(BillingPlan).where(BillingPlan.slug == "pro")
        )
        put = await client.put(
            f"/api/v1/billing/admin/users/{db_user.id}",
            json={"plan_id": pro.id, "exempt": True},
        )
        assert put.json()["plan_id"] == pro.id and put.json()["exempt"] is True
        ledger_rows = (
            await client.get(f"/api/v1/billing/admin/users/{db_user.id}/ledger")
        ).json()["items"]
        assert any(
            r["kind"] == "admin_adjust" and r["reason"] == "Goodwill"
            for r in ledger_rows
        )
        tiers = await client.put(
            "/api/v1/billing/admin/models/some-model", json={"tier": "premium"}
        )
        assert tiers.status_code == 200


async def test_admin_report_shows_shadow_costs(
    db_session, db_user, new_user, api, set_mode
):
    await set_mode("shadow")
    charge = await meter.begin(db_user.id, "studio_video")
    await meter.settle(charge, details={"provider_cost_micros": 250_000})
    admin = await new_user("owner@surfsense.net")
    async with api(admin) as client:
        report = (await client.get("/api/v1/billing/admin/report")).json()
    row = next(r for r in report["by_action"] if r["action"] == "studio_video")
    assert (
        row["mode"] == "shadow"
        and row["credits"] == 40
        and row["provider_cost_usd"] == 0.25
    )


async def test_the_public_site_catalog_has_the_sites_shape(api, set_mode):
    await set_mode("shadow")
    async with api(None) as client:
        response = await client.get("/api/v1/billing/public/catalog")
    body = response.json()
    assert response.headers["cache-control"] == "public, max-age=60"
    assert body["mode"] == "shadow" and body["currency"] == "USD"
    plus = next(p for p in body["plans"] if p["slug"] == "plus")
    assert plus == {
        **plus,
        "priceCents": 900,
        "monthlyCredits": 1500,
        "highlighted": True,
    }
    assert {"slug", "name", "credits", "priceCents"} <= set(body["packs"][0])
    video = next(p for p in body["prices"] if p["action"] == "studio_video")
    assert video == {"action": "studio_video", "label": "Video overview", "credits": 40, "unit": "output"}
