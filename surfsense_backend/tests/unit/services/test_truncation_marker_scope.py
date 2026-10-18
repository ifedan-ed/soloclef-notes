"""The cut-off marker reflects only output the user reads.

A subagent's reply is consumed by the main agent, which writes the answer
itself; a subagent hitting its output cap must not tell the user their
answer was cut off. Its tokens are still recorded and billed.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.agents.chat.multi_agent_chat.main_agent.middleware.checkpointed_subagent_middleware.task_tool import (
    _ainvoke_with_timeout,
)
from app.services.subagent_call_scope import is_inside_subagent, subagent_scope
from app.services.token_tracking_service import TokenTrackingCallback, scoped_turn

pytestmark = pytest.mark.unit


def _cut_off_response(completion: int = 2048) -> SimpleNamespace:
    return SimpleNamespace(
        usage=SimpleNamespace(
            prompt_tokens=15000,
            completion_tokens=completion,
            total_tokens=15000 + completion,
        ),
        choices=[SimpleNamespace(finish_reason="length")],
    )


async def _log(cb: TokenTrackingCallback) -> None:
    await cb.async_log_success_event(
        kwargs={"model": "some-model", "response_cost": 0.01},
        response_obj=_cut_off_response(),
        start_time=None,
        end_time=None,
    )


@pytest.mark.asyncio
async def test_main_agent_cut_off_sets_the_marker():
    async with scoped_turn() as acc:
        await _log(TokenTrackingCallback())
        assert acc.truncated is True


@pytest.mark.asyncio
async def test_subagent_cut_off_is_billed_but_not_marked():
    async with scoped_turn() as acc:
        with subagent_scope():
            await _log(TokenTrackingCallback())
        assert acc.truncated is False
        assert len(acc.calls) == 1
        assert acc.calls[0].completion_tokens == 2048
        assert acc.total_cost_micros == 10_000


@pytest.mark.asyncio
async def test_task_tool_runs_subagents_inside_the_scope():
    seen: list[bool] = []

    async def _subagent_run() -> str:
        seen.append(is_inside_subagent())
        return "done"

    assert is_inside_subagent() is False
    result = await _ainvoke_with_timeout(
        _subagent_run(), subagent_type="knowledge_base", started_at=0.0
    )
    assert result == "done"
    assert seen == [True]
    assert is_inside_subagent() is False
