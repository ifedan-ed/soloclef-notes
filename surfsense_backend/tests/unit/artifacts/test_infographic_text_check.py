"""An infographic whose image garbles its planned text is repaired once, then flagged."""

from __future__ import annotations

import hashlib
import io
import json
from contextlib import asynccontextmanager
from types import SimpleNamespace

import pytest
from langchain_core.messages import AIMessage
from PIL import Image

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools import (
    save_artifact as save_tool,
    verify_artifact as verify_tool,
)
from app.artifacts.infographic.selection import (
    InfographicGenerationState,
    write_generation_state,
)
from app.artifacts.infographic.text_check import (
    PASS_THRESHOLD,
    check_rendered_text,
    planned_items,
)
from tests.utils.fake_sandbox import FakeSandboxSession

pytestmark = pytest.mark.unit

SECRET = "secret"
MARKDOWN = """# P values: a dirty dozen

## What P is
- Evidence against the null: not its probability
- **Residents**: 88% confident, 62% correct

## Report instead
- Effect size
- Confidence interval (95%)
"""


def _png() -> bytes:
    out = io.BytesIO()
    Image.new("RGB", (400, 300), "white").save(out, "PNG")
    return out.getvalue()


class _Reader:
    """The vision model: marks the listed item ids with a status."""

    def __init__(self, statuses: dict[int, str]):
        self.statuses = statuses
        self.calls = 0

    async def ainvoke(self, messages):
        self.calls += 1
        n = len(planned_items(MARKDOWN))
        items = [
            {"id": i, "status": self.statuses.get(i, "present"), "seen": "88 %"}
            for i in range(1, n + 1)
        ]
        return AIMessage(content=json.dumps({"items": items}))


def test_the_planned_items_are_headings_labels_and_numbers():
    items = planned_items(MARKDOWN)

    assert items[:3] == [
        "P values: a dirty dozen",
        "What P is",
        "Evidence against the null",
    ]
    assert "Residents" in items and "88%" in items and "62%" in items
    assert "95%" in items and "Effect size" in items


async def test_a_clean_reading_scores_one():
    check = await check_rendered_text(_Reader({}), _png(), MARKDOWN)
    assert check.score == 1.0 and check.passed


async def test_garbled_and_missing_items_lower_the_score():
    items = planned_items(MARKDOWN)
    statuses = {items.index("88%") + 1: "garbled", 2: "missing", 3: "missing"}

    check = await check_rendered_text(_Reader(statuses), _png(), MARKDOWN)

    assert check.score == pytest.approx((len(items) - 3) / len(items))
    assert "What P is" in check.missing
    assert any("88%" in g and "88 %" in g for g in check.garbled)


async def test_no_vision_model_or_a_failing_one_never_blocks():
    assert (await check_rendered_text(None, _png(), MARKDOWN)).passed

    class Broken:
        async def ainvoke(self, _messages):
            raise TimeoutError

    check = await check_rendered_text(Broken(), _png(), MARKDOWN)
    assert check.passed and check.unavailable_reason == "TimeoutError"


@pytest.fixture
def verify_infographic(monkeypatch):
    session = FakeSandboxSession()
    png, md = _png(), MARKDOWN.encode()
    session.files["/workspace/ig.png"] = png
    session.files["/workspace/ig.md"] = md
    captured = {}

    class Registry:
        async def get_session(self, *_args):
            return session

    async def get_registry():
        return Registry()

    @asynccontextmanager
    async def db_session():
        yield object()

    async def verify(*_args, **kwargs):
        captured.update(kwargs)
        return SimpleNamespace(
            verified=True,
            findings=(),
            notes=(),
            page_count=None,
            unavailable_reason=None,
        )

    monkeypatch.setattr(verify_tool, "get_registry", get_registry)
    monkeypatch.setattr(verify_tool, "shielded_async_session", db_session)
    monkeypatch.setattr(verify_tool, "verify", verify)
    monkeypatch.setattr(verify_tool, "resolve_root_thread_id", lambda *_a: 11)
    monkeypatch.setattr(verify_tool.app_config, "SECRET_KEY", SECRET)

    async def run(attempt: int, reader):
        async def get_vision_llm(*_a, **_k):
            return reader

        monkeypatch.setattr(verify_tool, "get_vision_llm", get_vision_llm)
        await write_generation_state(
            session,
            InfographicGenerationState(
                workspace_id=7,
                session_id=session.session_id,
                output_path="/workspace/ig.png",
                selection_digest="d",
                attempts=attempt,
                png_sha256=hashlib.sha256(png).hexdigest(),
                markdown_sha256=hashlib.sha256(md).hexdigest(),
                requested_style_id="clay",
                resolved_style_id="clay",
                preset_id="p",
                preset_version=1,
                image_gen_model_id=160,
                provider_model="gpt-image-2",
                width=400,
                height=300,
                issued_at=1,
            ),
            secret_key=SECRET,
        )
        captured.clear()
        tool = verify_tool.create_verify_artifact_tool(workspace_id=7)
        result = await tool.coroutine(
            path="/workspace/ig.png",
            format="infographic",
            markdown_path="/workspace/ig.md",
            runtime=SimpleNamespace(),
        )
        return result, dict(captured)

    return run


def _garbling() -> _Reader:
    n = len(planned_items(MARKDOWN))
    return _Reader(dict.fromkeys(range(1, n // 2 + 2), "garbled"))


async def test_a_garbled_first_attempt_is_sent_back_for_its_repair(verify_infographic):
    result, captured = await verify_infographic(1, _garbling())

    assert result["status"] == "failed"
    assert any("Garbled in the image" in f for f in result["findings"])
    assert "repair_findings" in result["notes"][0]
    assert captured == {}  # no receipt: it cannot be saved as it is


async def test_a_garbled_repair_is_saved_with_the_result_recorded(verify_infographic):
    result, captured = await verify_infographic(2, _garbling())

    assert result["status"] == "verified"
    check = captured["provenance"]["text_check"]
    assert check["passed"] is False and check["score"] < PASS_THRESHOLD
    warning = save_tool._text_check_warning(captured["provenance"])
    assert warning.startswith("Only ") and "Garbled" not in warning
    assert "after one regeneration" in warning


async def test_a_legible_infographic_passes_with_its_score(verify_infographic):
    reader = _Reader({})
    result, captured = await verify_infographic(1, reader)

    assert result["status"] == "verified" and reader.calls == 1
    assert captured["provenance"]["text_check"]["score"] == 1.0
    assert save_tool._text_check_warning(captured["provenance"]) is None
