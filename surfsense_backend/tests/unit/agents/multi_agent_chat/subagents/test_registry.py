"""``build_subagents`` must drop user-disabled tools from every subagent spec,
and the ``ask_knowledge_base`` injection must respect the same disabled set.

The composer's "Manage Tools" sheet sends ``disabled_tools`` with each chat
request (see ``app.schemas.new_chat.NewChatRequest.disabled_tools``); this is
the module that actually removes a disabled tool from a built subagent's
tool list.
"""

from __future__ import annotations

import pytest

from app.agents.chat.multi_agent_chat.shared.permissions import Ruleset
from app.agents.chat.multi_agent_chat.subagents import registry as registry_module
from app.agents.chat.multi_agent_chat.subagents.shared.spec import SurfSenseSubagentSpec

pytestmark = pytest.mark.unit


class _FakeTool:
    """Minimal stand-in for a ``BaseTool``: only ``.name`` is read by the registry."""

    def __init__(self, name: str) -> None:
        self.name = name


def _fake_builder(name: str, tool_names: list[str]):
    def _build_subagent(*, dependencies, model=None, middleware_stack=None, mcp_tools=None):
        spec = {
            "name": name,
            "description": f"{name} route",
            "tools": [_FakeTool(n) for n in tool_names],
        }
        return SurfSenseSubagentSpec(spec=spec, ruleset=Ruleset(origin=name, rules=[]))

    return _build_subagent


@pytest.fixture
def two_fake_subagents(monkeypatch: pytest.MonkeyPatch) -> dict[str, list[str]]:
    """Patch the registry's builder table with two subagents carrying
    disjoint catalog tool names, so filtering can be asserted per-spec."""
    table = {
        "alpha": ["generate_podcast", "save_artifact"],
        "beta": ["execute", "generate_image"],
    }
    monkeypatch.setattr(
        registry_module,
        "SUBAGENT_BUILDERS_BY_NAME",
        {name: _fake_builder(name, tools) for name, tools in table.items()},
    )
    return table


def _tools_by_name(specs, name: str) -> list[str]:
    spec = next(s for s in specs if s["name"] == name)
    return [t.name for t in spec["tools"]]


def test_disabled_tool_is_removed_only_from_the_spec_that_had_it(two_fake_subagents) -> None:
    specs = registry_module.build_subagents(
        dependencies={}, disabled_tools=["generate_podcast"]
    )

    assert _tools_by_name(specs, "alpha") == ["save_artifact"]
    assert _tools_by_name(specs, "beta") == ["execute", "generate_image"]


def test_enabled_tool_set_is_unaffected_when_nothing_is_disabled(two_fake_subagents) -> None:
    specs = registry_module.build_subagents(dependencies={})

    assert _tools_by_name(specs, "alpha") == ["generate_podcast", "save_artifact"]
    assert _tools_by_name(specs, "beta") == ["execute", "generate_image"]


def test_disabling_an_unrelated_name_does_not_touch_other_tools(two_fake_subagents) -> None:
    specs = registry_module.build_subagents(
        dependencies={}, disabled_tools=["create_google_drive_file"]
    )

    assert _tools_by_name(specs, "alpha") == ["generate_podcast", "save_artifact"]
    assert _tools_by_name(specs, "beta") == ["execute", "generate_image"]


def test_ask_kb_tool_is_injected_into_every_other_spec_when_provided(
    two_fake_subagents,
) -> None:
    """Documents the injection mechanism that ``main_agent/middleware/stack.py``
    must gate on the disabled-tools set *before* calling ``build_subagents``
    (see ``test_stack.py::test_search_knowledge_base_disabled_*``) — this
    function itself injects unconditionally whenever it is handed a tool."""
    ask_kb_tool = _FakeTool("ask_knowledge_base")

    specs = registry_module.build_subagents(dependencies={}, ask_kb_tool=ask_kb_tool)

    assert "ask_knowledge_base" in _tools_by_name(specs, "alpha")
    assert "ask_knowledge_base" in _tools_by_name(specs, "beta")


def test_no_ask_kb_tool_injected_when_none_is_passed(two_fake_subagents) -> None:
    specs = registry_module.build_subagents(dependencies={}, ask_kb_tool=None)

    assert "ask_knowledge_base" not in _tools_by_name(specs, "alpha")
    assert "ask_knowledge_base" not in _tools_by_name(specs, "beta")


def test_knowledge_base_itself_is_excluded_from_builtin_ask_kb_injection(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """``knowledge_base``'s own spec never gets ``ask_knowledge_base`` appended
    (it would be a pointless self-call)."""

    def _kb_builder(*, dependencies, model=None, middleware_stack=None, mcp_tools=None):
        spec = {
            "name": "knowledge_base",
            "description": "kb",
            "tools": [_FakeTool("search_knowledge_base")],
        }
        return SurfSenseSubagentSpec(
            spec=spec, ruleset=Ruleset(origin="knowledge_base", rules=[])
        )

    monkeypatch.setattr(
        registry_module, "SUBAGENT_BUILDERS_BY_NAME", {"knowledge_base": _kb_builder}
    )

    specs = registry_module.build_subagents(
        dependencies={}, ask_kb_tool=_FakeTool("ask_knowledge_base")
    )

    assert _tools_by_name(specs, "knowledge_base") == ["search_knowledge_base"]
