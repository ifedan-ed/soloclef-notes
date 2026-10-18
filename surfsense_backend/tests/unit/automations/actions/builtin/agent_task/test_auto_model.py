"""Auto (id 0) becomes a concrete chat model for an automation run."""

from __future__ import annotations

import pytest

from app.automations.actions.builtin.agent_task.auto_model import (
    AutoModelUnavailableError,
    resolve_auto_chat_model,
)
from app.services import auto_model_pin_service as pins

pytestmark = pytest.mark.unit


def _offer(monkeypatch, candidates):
    async def fake_candidates(session, **kw):
        assert kw["capability"] == "chat"
        return [dict(c) for c in candidates]

    async def no_tiers(session, candidates):
        return None

    monkeypatch.setattr(pins, "auto_model_candidates", fake_candidates)
    monkeypatch.setattr(pins, "_apply_billing_tiers", no_tiers)


async def test_picks_a_standard_model_over_a_premium_one(monkeypatch):
    _offer(
        monkeypatch,
        [
            {"id": -3, "billing_tier": "premium", "quality_score": 99},
            {"id": -1, "billing_tier": "standard", "quality_score": 50},
        ],
    )
    for seed in range(5):
        assert (
            await resolve_auto_chat_model(None, workspace_id=1, user_id=None, seed=seed)
            == -1
        )


async def test_premium_only_is_still_better_than_nothing(monkeypatch):
    _offer(monkeypatch, [{"id": -3, "billing_tier": "premium"}])
    assert (
        await resolve_auto_chat_model(None, workspace_id=1, user_id=None, seed=1) == -3
    )


async def test_no_model_at_all_is_a_clear_error(monkeypatch):
    _offer(monkeypatch, [])
    with pytest.raises(AutoModelUnavailableError):
        await resolve_auto_chat_model(None, workspace_id=1, user_id=None, seed=1)
