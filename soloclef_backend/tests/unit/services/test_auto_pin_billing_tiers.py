"""Auto chooses by the tier billing charges, set on the billing admin page."""

from __future__ import annotations

from dataclasses import dataclass
from types import SimpleNamespace

import pytest

import app.services.auto_model_pin_service as svc
from app.config import config

pytestmark = pytest.mark.unit


class _Redis:
    def mget(self, keys):
        return [None for _ in keys]

    def set(self, *args, **kwargs):
        return True

    def scan_iter(self, pattern):
        return iter(())

    def delete(self, *keys):
        return 0


@dataclass
class _Quota:
    allowed: bool


class _Result:
    def __init__(self, thread=None):
        self._thread = thread

    def unique(self):
        return self

    def scalar_one_or_none(self):
        return self._thread

    def scalars(self):
        return SimpleNamespace(all=lambda: [])


class _Session:
    def __init__(self):
        self.thread = SimpleNamespace(id=1, workspace_id=10, pinned_llm_config_id=None)
        self.calls = 0

    async def execute(self, _stmt):
        self.calls += 1
        return _Result(self.thread if self.calls == 1 else None)

    async def commit(self):
        pass


def _shared(models: list[tuple[int, str, str]]):
    return [
        {
            "id": id_,
            "connection_id": -1,
            "model_id": name,
            "display_name": name,
            "supports_chat": True,
            "supports_image_input": True,
            "supports_tools": True,
            "supports_image_generation": False,
            "capabilities_override": {},
            "billing_tier": tier,
            "catalog": {"auto_pin_tier": "A", "quality_score": 50},
        }
        for id_, name, tier in models
    ]


@pytest.fixture
def catalog(monkeypatch):
    monkeypatch.setattr(svc, "_runtime_cooldown_redis", _Redis())
    monkeypatch.setattr(
        config,
        "GLOBAL_CONNECTIONS",
        [
            {
                "id": -1,
                "provider": "openai_compatible",
                "scope": "GLOBAL",
                "enabled": True,
            }
        ],
    )
    monkeypatch.setattr(config, "GLOBAL_LLM_CONFIGS", [])

    def use(models, *, premium_eligible: bool, overrides: dict[str, str]):
        monkeypatch.setattr(config, "GLOBAL_MODELS", _shared(models))

        async def quota(*_a, **_k):
            return _Quota(allowed=premium_eligible)

        async def tiers(_session):
            return overrides

        monkeypatch.setattr(svc.TokenQuotaService, "credit_get_usage", quota)
        monkeypatch.setattr("app.billing.model_tiers.tier_overrides", tiers)

    return use


async def _pick():
    return await svc.resolve_or_get_pinned_llm_config_id(
        _Session(),
        thread_id=1,
        workspace_id=10,
        user_id="00000000-0000-0000-0000-000000000001",
        selected_llm_config_id=0,
    )


async def test_a_model_the_admin_made_premium_is_not_picked_for_a_free_plan(catalog):
    catalog(
        [(-1, "cheap", "standard"), (-2, "fancy", "standard")],
        premium_eligible=False,
        overrides={"fancy": "premium"},
    )
    for _ in range(5):
        assert (await _pick()).resolved_llm_config_id == -1


async def test_a_premium_eligible_account_with_no_premium_model_still_gets_one(catalog):
    catalog(
        [(-1, "cheap", "standard"), (-2, "other", "standard")],
        premium_eligible=True,
        overrides={},
    )
    assert (await _pick()).resolved_llm_config_id in {-1, -2}


async def test_auto_picks_a_standard_model_even_for_a_premium_account(catalog):
    """Pro plan, Luna Pro marked premium by the admin: Auto still answers on
    the standard model (1 credit), never the premium one (5)."""
    catalog(
        [(-1, "cheap", "standard"), (-2, "fancy", "standard")],
        premium_eligible=True,
        overrides={"fancy": "premium"},
    )
    for _ in range(5):
        result = await _pick()
        assert (result.resolved_llm_config_id, result.resolved_tier) == (
            -1,
            "standard",
        )


async def test_premium_is_only_a_fallback_when_no_standard_model_is_up(catalog):
    catalog(
        [(-2, "fancy", "standard")],
        premium_eligible=True,
        overrides={"fancy": "premium"},
    )
    result = await _pick()
    assert (result.resolved_llm_config_id, result.resolved_tier) == (-2, "premium")


async def test_no_standard_model_and_no_premium_plan_means_no_auto_model(catalog):
    catalog(
        [(-2, "fancy", "standard")],
        premium_eligible=False,
        overrides={"fancy": "premium"},
    )
    with pytest.raises(ValueError):
        await _pick()


async def test_an_explicit_premium_pick_is_kept(catalog):
    catalog(
        [(-1, "cheap", "standard"), (-2, "fancy", "standard")],
        premium_eligible=True,
        overrides={"fancy": "premium"},
    )
    result = await svc.resolve_or_get_pinned_llm_config_id(
        _Session(),
        thread_id=1,
        workspace_id=10,
        user_id="00000000-0000-0000-0000-000000000001",
        selected_llm_config_id=-2,
    )
    assert (result.resolved_llm_config_id, result.resolved_tier) == (-2, "explicit")
