"""A verb that cannot run here (no key, key refused, provider busy) tells the
model in words, is recorded as a failed run, and charges nothing; any other
executor failure still raises."""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from pydantic import BaseModel

from app.capabilities.core.types import BillingUnit, Capability
from app.capabilities.core.unavailable import CapabilityUnavailableError

pytestmark = pytest.mark.unit


class _Input(BaseModel):
    query: str

    @property
    def estimated_units(self) -> int:
        return 1


class _Output(BaseModel):
    @property
    def billable_units(self) -> int:
        return 1


class _Session:
    async def __aenter__(self):
        return SimpleNamespace()

    async def __aexit__(self, *exc):
        return False


def _capability(error: Exception) -> Capability:
    async def executor(_payload):
        raise error

    return Capability(
        name="exa.web_search",
        description="Search the web with Exa.",
        input_schema=_Input,
        output_schema=_Output,
        executor=executor,
        billing_unit=BillingUnit.EXA_SEARCH,
    )


@pytest.fixture
def door(monkeypatch):
    from app.capabilities.core.access import agent as mod

    monkeypatch.setattr(mod, "async_session_maker", lambda: _Session())
    charge = AsyncMock()
    record = AsyncMock(return_value=None)
    monkeypatch.setattr(mod, "gate_capability", AsyncMock())
    monkeypatch.setattr(mod, "charge_capability", charge)
    monkeypatch.setattr(mod, "record_run", record)
    return SimpleNamespace(module=mod, charge=charge, record=record)


def _tool(door, error: Exception):
    tools = door.module.build_capability_tools(
        workspace_id=7, capabilities=[_capability(error)]
    )
    return next(t for t in tools if t.name == "exa_web_search")


def _runtime():
    return SimpleNamespace(state={}, tool_call_id="tc_1", context=None)


async def test_an_unavailable_verb_answers_with_its_sentence_and_charges_nothing(
    door,
):
    tool = _tool(door, CapabilityUnavailableError("Exa isn't connected here."))

    result = await tool.coroutine(_runtime(), query="q")

    assert result == "Exa isn't connected here."
    door.charge.assert_not_awaited()
    (call,) = door.record.await_args_list
    assert call.kwargs["status"] == "error"
    assert call.kwargs["capability"] == "exa.web_search"


async def test_any_other_failure_still_raises(door):
    tool = _tool(door, RuntimeError("boom"))

    with pytest.raises(RuntimeError, match="boom"):
        await tool.coroutine(_runtime(), query="q")
    door.charge.assert_not_awaited()
