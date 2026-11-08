"""Orientation, level of detail and style reach the image model concretely."""

from __future__ import annotations

import json
from contextlib import asynccontextmanager
from types import SimpleNamespace

import pytest

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools import (
    sandbox as sandbox_tools,
)
from app.artifacts.infographic import generation as generation_module
from app.artifacts.infographic.generation import GeneratedInfographic
from app.artifacts.infographic.layout import (
    InfographicLayout,
    canvas_for,
    detail_for,
    image_directions,
)
from app.artifacts.infographic.presets import (
    QUESTION_PRESET_ID,
    VISUAL_STYLE_PRESETS,
    resolve_visual_style,
)
from app.artifacts.infographic.prompt import assemble_infographic_prompt
from app.artifacts.infographic.selection import (
    issue_selection_token,
    read_generation_state,
    read_selection_token,
)
from app.artifacts.infographic.text_check import check_rendered_text, planned_items
from tests.utils.fake_sandbox import FakeSandboxSession

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("orientation", "size"),
    [
        ("landscape", "1536x1024"),
        ("portrait", "1024x1536"),
        ("square", "1024x1024"),
    ],
)
def test_each_orientation_is_a_size_the_image_model_accepts(orientation, size):
    canvas = canvas_for(orientation)
    assert canvas is not None and canvas.size == size
    assert f"{canvas.width}x{canvas.height}" in canvas.composition


def test_no_orientation_keeps_the_models_default_and_unknown_is_refused():
    assert canvas_for(None) is None
    with pytest.raises(ValueError, match="orientation"):
        canvas_for("panorama")
    with pytest.raises(ValueError, match="level of detail"):
        detail_for("exhaustive")
    with pytest.raises(ValueError):
        InfographicLayout(orientation="wide")


def test_detail_changes_the_plan_and_how_many_items_are_checked():
    concise, standard, detailed = (
        detail_for(level) for level in ("concise", "standard", "detailed")
    )
    assert concise and standard and detailed
    assert (
        concise.max_checked_items
        < standard.max_checked_items
        < detailed.max_checked_items
    )
    assert "Plan 3 H2 sections" in concise.plan()
    assert "Plan 6 or 7 H2 sections" in detailed.plan()
    assert concise.density != detailed.density


def test_every_style_has_its_own_concrete_description():
    descriptions = [preset.description for preset in VISUAL_STYLE_PRESETS]
    assert len(set(descriptions)) == len(descriptions)
    for preset in VISUAL_STYLE_PRESETS:
        # A specific description, not just the style's name.
        assert len(preset.description) > 150
        assert len(preset.description) <= 600


def test_image_prompt_carries_style_canvas_density_and_the_users_words():
    style = resolve_visual_style("bento-grid", "").preset
    prompt = assemble_infographic_prompt(
        factual_content="# Iron\n## Sources\n- Spinach",
        style=style,
        directions=image_directions(
            orientation="portrait",
            detail="concise",
            description="Use a blue colour theme",
        ),
    )
    assert "rounded rectangular tiles" in prompt
    assert "Portrait canvas, 1024x1536 pixels" in prompt
    assert "Concise:" in prompt
    assert "THE USER ASKED FOR\nUse a blue colour theme" in prompt
    # Nothing chosen (a chat run): the prompt is as before.
    plain = assemble_infographic_prompt(
        factual_content="# Iron", style=style, directions=None
    )
    assert "CANVAS" not in plain and "LEVEL OF DETAIL" not in plain


async def test_generation_asks_the_image_model_for_the_orientations_size(monkeypatch):
    seen = {}

    async def fake_response(_session, **kwargs):
        seen.update(kwargs)
        return SimpleNamespace(response={}, config_id=160, provider_model="gpt-image-2")

    async def fake_bytes(_response):
        return b"png", "image/png", "png"

    monkeypatch.setattr(generation_module, "generate_image_response", fake_response)
    monkeypatch.setattr(generation_module, "image_bytes_from_response", fake_bytes)
    monkeypatch.setattr(
        generation_module,
        "normalize_infographic_png",
        lambda _raw: (b"\x89PNG", 1536, 1024),
    )

    result = await generation_module.generate_infographic(
        object(),
        workspace_id=1,
        factual_content="# Iron\n## Sources",
        style=resolve_visual_style("scientific", "").preset,
        orientation="landscape",
        detail="detailed",
        description="Highlight the 3 key stats",
    )

    assert seen["size"] == "1536x1024"
    assert "Landscape canvas" in seen["prompt"]
    assert "Detailed:" in seen["prompt"]
    assert "Highlight the 3 key stats" in seen["prompt"]
    assert (result.width, result.height) == (1536, 1024)

    seen.clear()
    await generation_module.generate_infographic(
        object(),
        workspace_id=1,
        factual_content="# Iron\n## Sources",
        style=resolve_visual_style("clay", "").preset,
    )
    assert seen["size"] is None


def test_the_signed_selection_carries_the_layout_and_old_tokens_still_read():
    resolved = resolve_visual_style("editorial", "")
    token = issue_selection_token(
        workspace_id=1,
        thread_id=2,
        preset_id=QUESTION_PRESET_ID,
        preset_version=1,
        resolved=resolved,
        secret_key="s",
        orientation="square",
        detail="concise",
    )
    selection = read_selection_token(token, workspace_id=1, thread_id=2, secret_key="s")
    assert (selection.orientation, selection.detail) == ("square", "concise")

    chat = issue_selection_token(
        workspace_id=1,
        thread_id=2,
        preset_id=QUESTION_PRESET_ID,
        preset_version=1,
        resolved=resolved,
        secret_key="s",
    )
    selection = read_selection_token(chat, workspace_id=1, thread_id=2, secret_key="s")
    assert (selection.orientation, selection.detail) == (None, None)

    with pytest.raises(ValueError):
        issue_selection_token(
            workspace_id=1,
            thread_id=2,
            preset_id=QUESTION_PRESET_ID,
            preset_version=1,
            resolved=resolved,
            secret_key="s",
            orientation="panorama",
        )


@asynccontextmanager
async def _db_session():
    yield object()


async def test_generation_follows_the_signed_layout_not_the_model(monkeypatch):
    session = FakeSandboxSession()
    token = issue_selection_token(
        workspace_id=7,
        thread_id=11,
        preset_id=QUESTION_PRESET_ID,
        preset_version=1,
        resolved=resolve_visual_style("professional", ""),
        secret_key="secret",
        orientation="portrait",
        detail="detailed",
    )
    calls = []

    async def fake_generate(_session, **kwargs):
        calls.append(kwargs)
        return GeneratedInfographic(
            png=b"\x89PNG\r\n\x1a\nimage",
            width=1024,
            height=1536,
            image_gen_model_id=160,
            provider_model="gpt-image-2",
        )

    monkeypatch.setattr(sandbox_tools.app_config, "SECRET_KEY", "secret")
    monkeypatch.setattr(sandbox_tools, "shielded_async_session", _db_session)
    monkeypatch.setattr(sandbox_tools, "generate_infographic", fake_generate)

    result = json.loads(
        await sandbox_tools._generate_infographic_file(
            session=session,
            workspace_id=7,
            thread_id=11,
            factual_markdown="# Guide\n\n## One\nFirst.",
            output_path="/workspace/guide.png",
            selection_token=token,
            output_constraints=None,
            repair_findings=None,
            description="Blue theme",
        )
    )

    assert calls[0]["orientation"] == "portrait"
    assert calls[0]["detail"] == "detailed"
    assert calls[0]["description"] == "Blue theme"
    assert result["orientation"] == "portrait"
    state = await read_generation_state(
        session, "/workspace/guide.png", workspace_id=7, secret_key="secret"
    )
    assert state is not None
    provenance = state.manifest_provenance()
    assert (provenance["orientation"], provenance["detail"]) == ("portrait", "detailed")
    assert (provenance["width"], provenance["height"]) == (1024, 1536)


async def test_studio_instructions_sign_the_layout_and_describe_the_plan(monkeypatch):
    session = FakeSandboxSession({})

    async def run_command(_command):
        return sandbox_tools.ExecResult("# Infographics skill", 0)

    session.run_command = run_command  # type: ignore[method-assign]

    async def get_session(*_args):
        return session

    monkeypatch.setattr(sandbox_tools, "_get_session", get_session)
    monkeypatch.setattr(sandbox_tools, "resolve_root_thread_id", lambda *_: 4)
    monkeypatch.setattr(sandbox_tools.app_config, "SECRET_KEY", "secret")
    tool = next(
        tool
        for tool in sandbox_tools.create_sandbox_tools(
            workspace_id=3,
            preset_infographic_style="bricks",
            preset_infographic_layout=InfographicLayout(
                orientation="square", detail="concise"
            ),
        )
        if tool.name == "load_artifact_instructions"
    )

    text = await tool.coroutine(
        artifact_type="infographic",
        runtime=SimpleNamespace(),
        brief="Toys",
    )

    assert "square page (1:1)" in text
    assert "Plan 3 H2 sections" in text
    handle = text.split("infographic_selection_token: ", 1)[1].split("\n", 1)[0]
    token = await sandbox_tools.resolve_selection(session, handle)
    selection = read_selection_token(
        token, workspace_id=3, thread_id=4, secret_key="secret"
    )
    assert (selection.resolved_style_id, selection.orientation, selection.detail) == (
        "bricks",
        "square",
        "concise",
    )


def test_planned_items_follow_the_detail_limit():
    markdown = "# Title\n" + "\n".join(
        f"## Section {i}\n- Label {i}" for i in range(20)
    )
    assert len(planned_items(markdown)) == 30
    assert len(planned_items(markdown, 12)) == 12


async def test_the_text_check_reads_back_only_the_detail_levels_items(monkeypatch):
    from app.artifacts.infographic import text_check

    asked = {}

    async def fake_invoke(_llm, messages, _schema, **_kwargs):
        prompt = messages[0].content[0]["text"]
        items = prompt.split("Planned items:\n", 1)[1].split("\n\n", 1)[0].splitlines()
        asked["items"] = len(items)
        return text_check._Reading(
            items=[
                text_check._ItemRead(id=i, status="present")
                for i in range(1, len(items) + 1)
            ]
        )

    monkeypatch.setattr(text_check, "invoke_json", fake_invoke)
    monkeypatch.setattr(text_check, "_jpeg_data_url", lambda _png: "data:")
    markdown = "# Title\n" + "\n".join(
        f"## Section {i}\n- Label {i}" for i in range(20)
    )

    result = await check_rendered_text(object(), b"png", markdown, max_items=12)

    assert asked["items"] == 12
    assert result.checked == 12 and result.passed
