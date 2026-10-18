"""Unit tests for the ``billable_call`` async context manager.

Credit comes from ``app.billing.meter`` at the action's flat price:

* No ``action``: nothing is metered (the call is part of something already
  charged), but a ``TokenUsage`` audit row records the provider cost.
* An ``action``: the price is held before the call, settled after it, released
  if it raises; a refusal raises ``QuotaInsufficientError`` before it runs.
* The audit row is best-effort and bounded by a timeout.
"""

from __future__ import annotations

import asyncio
import contextlib
from typing import Any
from uuid import uuid4

import pytest

from app.billing.errors import BillingDenied
from app.billing.meter import Charge

pytestmark = pytest.mark.unit


class _FakeSession:
    async def commit(self) -> None:
        pass

    async def rollback(self) -> None:
        pass


@contextlib.asynccontextmanager
async def _fake_sessions():
    yield _FakeSession()


def _patch(
    monkeypatch, *, deny: str | None = None, settle_exc: Exception | None = None
):
    calls: dict[str, list[Any]] = {
        "begin": [],
        "settle": [],
        "release": [],
        "record": [],
    }

    async def begin(user_id, action, **kwargs):
        calls["begin"].append({"user_id": user_id, "action": action, **kwargs})
        if deny:
            raise BillingDenied(
                deny, action=action, required_micros=40_000, available_micros=5
            )
        return Charge(usage_id=7, mode="live", action=action, micros=40_000)

    async def settle(charge, **kwargs):
        if settle_exc is not None:
            raise settle_exc
        calls["settle"].append((charge, kwargs))
        return charge.micros

    async def release(charge, **kwargs):
        calls["release"].append((charge, kwargs))

    async def record(session, **kwargs):
        calls["record"].append(kwargs)

    monkeypatch.setattr("app.billing.meter.begin", begin)
    monkeypatch.setattr("app.billing.meter.settle", settle)
    monkeypatch.setattr("app.billing.meter.release", release)
    monkeypatch.setattr("app.services.billable_calls.record_token_usage", record)
    monkeypatch.setattr(
        "app.services.billable_calls.shielded_async_session", _fake_sessions
    )
    return calls


def _spend(acc, cost=37_000):
    acc.add(
        model="openai/gpt-image-1",
        prompt_tokens=0,
        completion_tokens=0,
        total_tokens=0,
        cost_micros=cost,
        call_kind="image_generation",
    )


async def test_a_call_inside_a_charged_action_is_audited_not_metered(monkeypatch):
    from app.services.billable_calls import billable_call

    calls = _patch(monkeypatch)
    async with billable_call(
        user_id=uuid4(), workspace_id=42, usage_type="podcast_generation"
    ) as acc:
        _spend(acc)
    assert calls["begin"] == calls["settle"] == calls["release"] == []
    assert calls["record"][0]["usage_type"] == "podcast_generation"
    assert calls["record"][0]["cost_micros"] == 37_000


async def test_an_action_is_held_then_settled_with_the_provider_cost(monkeypatch):
    from app.services.billable_calls import billable_call

    calls = _patch(monkeypatch)
    user_id = uuid4()
    async with billable_call(
        user_id=user_id,
        workspace_id=42,
        usage_type="image_generation",
        action="image_generation",
        units=2,
    ) as acc:
        _spend(acc, 40_000)
    assert calls["begin"][0]["action"] == "image_generation"
    assert calls["begin"][0]["units"] == 2
    charge, kwargs = calls["settle"][0]
    assert charge.usage_id == 7 and kwargs["details"]["provider_cost_micros"] == 40_000
    assert calls["release"] == [] and len(calls["record"]) == 1


async def test_a_refusal_raises_before_the_call_runs(monkeypatch):
    from app.services.billable_calls import QuotaInsufficientError, billable_call

    calls = _patch(monkeypatch, deny="PLAN_VISION_NOT_INCLUDED")
    ran = False
    with pytest.raises(QuotaInsufficientError) as denied:
        async with billable_call(
            user_id=uuid4(),
            workspace_id=42,
            usage_type="vision_extraction",
            action="vision_image",
        ):
            ran = True
    assert not ran
    assert denied.value.code == "PLAN_VISION_NOT_INCLUDED"
    assert calls["settle"] == []


async def test_a_failed_call_gives_its_hold_back_and_is_still_audited(monkeypatch):
    from app.services.billable_calls import billable_call

    calls = _patch(monkeypatch)
    with pytest.raises(RuntimeError):
        async with billable_call(
            user_id=uuid4(),
            workspace_id=42,
            usage_type="image_generation",
            action="image_generation",
        ):
            raise RuntimeError("provider down")
    assert len(calls["release"]) == 1 and calls["settle"] == []
    assert len(calls["record"]) == 1


async def test_a_settlement_failure_is_raised(monkeypatch):
    from app.services.billable_calls import BillingSettlementError, billable_call

    _patch(monkeypatch, settle_exc=RuntimeError("db gone"))
    with pytest.raises(BillingSettlementError):
        async with billable_call(
            user_id=uuid4(),
            workspace_id=42,
            usage_type="image_generation",
            action="image_generation",
        ):
            pass


async def test_a_hanging_audit_commit_times_out(monkeypatch):
    from app.services.billable_calls import billable_call

    calls = _patch(monkeypatch)

    class _Hanging(_FakeSession):
        async def commit(self) -> None:
            await asyncio.sleep(60)

    @contextlib.asynccontextmanager
    async def hanging():
        yield _Hanging()

    async with billable_call(
        user_id=uuid4(),
        workspace_id=42,
        usage_type="image_generation",
        action="image_generation",
        billable_session_factory=hanging,
        audit_timeout_seconds=0.01,
    ) as acc:
        _spend(acc)
    assert len(calls["settle"]) == 1


async def test_audit_failure_is_best_effort(monkeypatch):
    from app.services.billable_calls import billable_call

    _patch(monkeypatch)

    async def broken(session, **kwargs):
        raise RuntimeError("audit table locked")

    monkeypatch.setattr("app.services.billable_calls.record_token_usage", broken)
    async with billable_call(
        user_id=uuid4(), workspace_id=42, usage_type="image_generation"
    ) as acc:
        _spend(acc)
