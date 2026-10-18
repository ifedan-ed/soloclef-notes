"""Where a turn's document scope is read from, and that nothing can widen it."""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.agents.chat.document_scope import (
    CONFIG_KEY,
    document_scope_of,
    in_scope,
    restrict,
    scope_config_value,
)
from app.agents.chat.multi_agent_chat.subagents.shared.invocation import (
    subagent_invoke_config,
)
from app.agents.chat.shared.context import SurfSenseContextSchema

pytestmark = pytest.mark.unit


def _runtime(*, context=None, configurable=None):
    return SimpleNamespace(
        context=context,
        config={"configurable": dict(configurable or {})},
        tool_call_id="call-1",
    )


def test_unscoped_by_default():
    assert document_scope_of(_runtime()) is None
    assert document_scope_of(_runtime(context=SurfSenseContextSchema())) is None


def test_read_from_the_main_graph_context():
    ctx = SurfSenseContextSchema(document_scope=[3, 1])
    assert document_scope_of(_runtime(context=ctx)) == frozenset({1, 3})


def test_read_from_the_run_config_in_a_subagent():
    runtime = _runtime(configurable={CONFIG_KEY: [5]})
    assert document_scope_of(runtime) == frozenset({5})


def test_an_empty_scope_is_not_unrestricted():
    assert document_scope_of(_runtime(configurable={CONFIG_KEY: []})) == frozenset()
    assert not in_scope(frozenset(), 1)


def test_context_and_config_are_intersected():
    ctx = SurfSenseContextSchema(document_scope=[1, 2])
    runtime = _runtime(context=ctx, configurable={CONFIG_KEY: [2, 3]})
    assert document_scope_of(runtime) == frozenset({2})


def test_nested_subagent_invocations_inherit_the_scope():
    """``task`` and ``ask_knowledge_base`` both build the child config this way."""
    parent = _runtime(configurable={"thread_id": "7", CONFIG_KEY: [4]})
    child_config = subagent_invoke_config(parent)
    grandchild_config = subagent_invoke_config(
        SimpleNamespace(config=child_config, tool_call_id="call-2")
    )
    child = SimpleNamespace(context=None, config=grandchild_config)
    assert document_scope_of(child) == frozenset({4})


def test_helpers():
    assert restrict(None, [3, 1]) == [3, 1]
    assert restrict(frozenset({1}), [3, 1]) == [1]
    assert in_scope(None, None)
    assert not in_scope(frozenset({1}), None)
    assert scope_config_value(None) is None
    assert scope_config_value([3, 1, 3]) == [1, 3]
