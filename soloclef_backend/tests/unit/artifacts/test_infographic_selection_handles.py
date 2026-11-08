"""A model copies a short selection handle; a wrong one is retryable, not fatal."""

from __future__ import annotations

import json
from contextlib import asynccontextmanager
from types import SimpleNamespace

import pytest

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools import (
    sandbox as sandbox_tools,
)
from app.artifacts.infographic.generation import GeneratedInfographic
from app.artifacts.infographic.presets import QUESTION_PRESET_ID, resolve_visual_style
from app.artifacts.infographic.selection import issue_selection_token
from app.artifacts.infographic.selection_handles import (
    InfographicSelectionError,
    remember_selection,
    resolve_selection,
)
from app.sandbox import ExecResult
from tests.utils.fake_sandbox import FakeSandboxSession

pytestmark = pytest.mark.unit

SECRET = "secret"


@asynccontextmanager
async def _db_session():
    yield object()


def _token(style: str = "clay", thread_id: int = 11) -> str:
    return issue_selection_token(
        workspace_id=7,
        thread_id=thread_id,
        preset_id=QUESTION_PRESET_ID,
        preset_version=1,
        resolved=resolve_visual_style(style, ""),
        secret_key=SECRET,
    )


@pytest.fixture
def generation(monkeypatch):
    calls: list[dict] = []

    async def fake_generate(_session, **kwargs):
        calls.append(kwargs)
        return GeneratedInfographic(
            png=b"\x89PNG\r\n\x1a\nimage",
            width=1200,
            height=800,
            image_gen_model_id=17,
            provider_model="provider/model",
        )

    monkeypatch.setattr(sandbox_tools.app_config, "SECRET_KEY", SECRET)
    monkeypatch.setattr(sandbox_tools, "shielded_async_session", _db_session)
    monkeypatch.setattr(sandbox_tools, "generate_infographic", fake_generate)
    return calls


def _execute_tool(monkeypatch, session):
    async def get_session(*_args):
        return session

    monkeypatch.setattr(sandbox_tools, "_get_session", get_session)
    monkeypatch.setattr(sandbox_tools, "resolve_root_thread_id", lambda *_: 11)
    return next(
        t
        for t in sandbox_tools.create_sandbox_tools(workspace_id=7)
        if t.name == "execute"
    )


async def _generate(session, selection, **kwargs):
    return json.loads(
        await sandbox_tools._generate_infographic_file(
            session=session,
            workspace_id=7,
            thread_id=11,
            factual_markdown="# Guide\n\n## One\nFirst.",
            output_path="/workspace/guide.png",
            selection_token=selection,
            output_constraints=None,
            repair_findings=kwargs.get("repair_findings"),
        )
    )


async def test_the_handle_is_short_and_generates(generation):
    session = FakeSandboxSession()
    handle = await remember_selection(
        session, _token(), requested_style_id="clay", resolved_style_id="clay"
    )

    assert handle.startswith("sel-") and len(handle) < 20
    result = await _generate(session, handle)

    assert result["status"] == "generated"
    assert result["resolved_style_id"] == "clay"


async def test_the_handle_and_its_full_token_are_the_same_selection_for_a_repair(
    generation,
):
    session = FakeSandboxSession()
    token = _token()
    handle = await remember_selection(
        session, token, requested_style_id="clay", resolved_style_id="clay"
    )

    await _generate(session, token)
    repaired = await _generate(session, handle, repair_findings=["Clipped title."])

    assert repaired["attempt"] == 2


@pytest.mark.parametrize("value", ["clay", " `SEL-{h}` ", '"sel-{h}"'])
async def test_a_style_id_or_a_lightly_mangled_handle_is_accepted(value):
    session = FakeSandboxSession()
    token = _token()
    handle = await remember_selection(
        session, token, requested_style_id="auto", resolved_style_id="clay"
    )

    assert await resolve_selection(session, value.format(h=handle[4:])) == token


async def test_a_mistyped_token_is_a_retryable_tool_error_listing_the_options(
    monkeypatch, generation
):
    session = FakeSandboxSession()
    token = _token()
    handle = await remember_selection(
        session, token, requested_style_id="clay", resolved_style_id="clay"
    )
    mistyped = token[:100] + ("A" if token[100] != "A" else "B") + token[101:]
    execute = _execute_tool(monkeypatch, session)

    reply = json.loads(
        await execute.coroutine(
            code_or_command="# Guide\n\n## One\nFirst.",
            runtime=SimpleNamespace(),
            language="infographic",
            output_path="/workspace/guide.png",
            infographic_selection_token=mistyped,
        )
    )

    assert reply["status"] == "error"
    assert reply["retryable"] is True
    assert reply["valid_selections"] == [{"handle": handle, "style": "clay"}]
    assert generation == []

    retried = json.loads(
        await execute.coroutine(
            code_or_command="# Guide\n\n## One\nFirst.",
            runtime=SimpleNamespace(),
            language="infographic",
            output_path="/workspace/guide.png",
            infographic_selection_token=handle,
        )
    )
    assert retried["status"] == "generated"


async def test_a_token_from_another_thread_is_retryable_too(monkeypatch, generation):
    session = FakeSandboxSession()
    await remember_selection(
        session, _token(), requested_style_id="clay", resolved_style_id="clay"
    )
    execute = _execute_tool(monkeypatch, session)

    reply = json.loads(
        await execute.coroutine(
            code_or_command="# Guide\n\n## One\nFirst.",
            runtime=SimpleNamespace(),
            language="infographic",
            output_path="/workspace/guide.png",
            infographic_selection_token=_token(thread_id=99),
        )
    )

    assert reply["retryable"] is True
    assert "another workspace or thread" in reply["error"]
    assert len(reply["valid_selections"]) == 1


async def test_an_unknown_handle_with_nothing_remembered_says_to_reload(
    monkeypatch, generation
):
    execute = _execute_tool(monkeypatch, FakeSandboxSession())

    reply = json.loads(
        await execute.coroutine(
            code_or_command="# Guide\n\n## One\nFirst.",
            runtime=SimpleNamespace(),
            language="infographic",
            output_path="/workspace/guide.png",
            infographic_selection_token="sel-0000000000",
        )
    )

    assert reply["retryable"] is True
    assert reply["valid_selections"] == []
    assert "load_artifact_instructions" in reply["hint"]


async def test_other_generation_errors_still_raise(monkeypatch, generation):
    execute = _execute_tool(monkeypatch, FakeSandboxSession())

    with pytest.raises(ValueError, match="under /workspace"):
        await execute.coroutine(
            code_or_command="# Guide",
            runtime=SimpleNamespace(),
            language="infographic",
            output_path="/tmp/guide.png",
            infographic_selection_token="sel-0000000000",
        )


async def test_mismatch_is_a_value_error_for_callers_that_expect_one():
    assert issubclass(InfographicSelectionError, ValueError)


async def test_the_instructions_give_the_model_the_handle_not_the_token(monkeypatch):
    session = FakeSandboxSession(
        command_handler=lambda _c: ExecResult("# Infographics skill", 0)
    )

    async def get_session(*_args):
        return session

    monkeypatch.setattr(sandbox_tools, "_get_session", get_session)
    monkeypatch.setattr(sandbox_tools, "resolve_root_thread_id", lambda *_: 11)
    monkeypatch.setattr(sandbox_tools.app_config, "SECRET_KEY", SECRET)
    tool = next(
        t
        for t in sandbox_tools.create_sandbox_tools(
            workspace_id=7, preset_infographic_style="clay"
        )
        if t.name == "load_artifact_instructions"
    )

    text = await tool.coroutine(
        artifact_type="infographic", runtime=SimpleNamespace(), brief="Iron"
    )

    handle = text.split("infographic_selection_token: ", 1)[1].split("\n", 1)[0]
    assert handle.startswith("sel-") and len(handle) == 14
    assert "." not in handle
    assert len(text.split("Trusted infographic selection:", 1)[1]) < 400
