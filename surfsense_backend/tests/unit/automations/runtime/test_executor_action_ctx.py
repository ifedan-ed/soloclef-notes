"""Lock that the executor propagates the captured model snapshot into the
``ActionContext``, so runs resolve their own model (insulated from chat /
workspace changes) and not the live workspace.
"""

from __future__ import annotations

from types import SimpleNamespace
from typing import cast

import pytest
from sqlalchemy.ext.asyncio import AsyncSession

from app.automations.runtime.executor import _build_action_ctx
from app.automations.schemas.definition.envelope import AutomationModels
from app.automations.schemas.definition.plan_step import PlanStep

pytestmark = pytest.mark.unit


def _run() -> SimpleNamespace:
    return SimpleNamespace(
        id=1,
        automation=SimpleNamespace(
            id=7, workspace_id=42, created_by_user_id="u-1", name="Test automation"
        ),
        trigger=None,
    )


def test_build_action_ctx_propagates_captured_models() -> None:
    """``definition.models`` flows onto the ActionContext model fields."""
    models = AutomationModels(
        chat_model_id=-1,
        image_gen_model_id=5,
        vision_model_id=-1,
    )
    ctx = _build_action_ctx(
        cast(AsyncSession, None),
        _run(),
        PlanStep(step_id="s1", action="agent_task"),
        models,
    )

    assert ctx.workspace_id == 42
    assert ctx.chat_model_id == -1
    assert ctx.image_gen_model_id == 5
    assert ctx.vision_model_id == -1
    assert ctx.automation_name == "Test automation"
    # What the run writes is labelled with its automation (the Notes page).
    assert ctx.automation_id == 7
    assert ctx.run_timezone is None


def test_build_action_ctx_carries_the_schedule_triggers_timezone() -> None:
    """A schedule trigger's timezone flows onto ``run_timezone`` (feature 2's
    "local date" note title, feature 3's subject date)."""
    run = _run()
    run.trigger = SimpleNamespace(params={"timezone": "Europe/Paris"})
    ctx = _build_action_ctx(
        cast(AsyncSession, None), run, PlanStep(step_id="s1", action="agent_task"), None
    )
    assert ctx.run_timezone == "Europe/Paris"


def test_build_action_ctx_none_models_leaves_fields_none() -> None:
    """No captured snapshot → model fields are None (defensive fallback path)."""
    ctx = _build_action_ctx(
        cast(AsyncSession, None),
        _run(),
        PlanStep(step_id="s1", action="agent_task"),
        None,
    )

    assert ctx.chat_model_id is None
    assert ctx.image_gen_model_id is None
    assert ctx.vision_model_id is None
