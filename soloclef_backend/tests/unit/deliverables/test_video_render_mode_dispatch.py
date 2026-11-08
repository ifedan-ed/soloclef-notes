"""The executor renders the way VIDEO_RENDER_MODE says, and only that way.

Rolling back has to be one environment variable, so the two paths must not
share a step: nothing from the stills assembly may run under ``frames``, and
the full-frame render must not run under ``stills``.
"""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

from app.artifacts.service import ArtifactSaved
from app.deliverables.video import executor
from tests.utils.fake_sandbox import FakeSandboxSession

pytestmark = pytest.mark.unit

_NARRATION = [
    {"slide_number": 1, "audio": "slide-1.mp3", "duration_seconds": 22.248},
    {"slide_number": 2, "audio": "slide-2.mp3", "duration_seconds": 11.04},
]


def _job():
    return SimpleNamespace(
        id=7,
        kind="video",
        title="Quarterly update",
        workspace_id=3,
        thread_id=11,
        tool_call_id="tool-1",
        celery_task_id="deliverable-job:7:attempt:1",
        attempt_count=1,
        request={
            "version": 1,
            "brief": "Explain the quarter",
            "source_references": [],
            "revision_artifact_id": None,
            "root_thread_id": 11,
        },
    )


def _authored():
    return executor.AuthoredVideo(
        scenes=[
            executor.AuthoredVideoScene(
                slide_number=number,
                filename=f"scene-{number}.tsx",
                on_screen_markdown=f"Scene {number}",
                transcript=f"Narration {number}",
                code=f"export default () => <div>{number}</div>;",
            )
            for number in (1, 2)
        ]
    )


@pytest.fixture
def pipeline(monkeypatch):
    """Everything but the render step, with both render paths recorded."""
    taken: list[str] = []
    sandbox = FakeSandboxSession()

    class Registry:
        async def get_session(self, *_args):
            return sandbox

    async def get_registry():
        return Registry()

    async def author(*_args):
        return _authored()

    async def narration(*_args, **_kwargs):
        return _NARRATION

    async def prepare(*_args, **_kwargs):
        return {"props_path": "/workspace/deliverable-job-7-attempt-1/props.json"}

    async def noop(*_args, **_kwargs):
        return None

    async def frames_render(*_args, **_kwargs):
        taken.append("frames")

    async def stills_assembly(*_args, **_kwargs):
        taken.append("stills")

    async def verify(*_args, **_kwargs):
        return SimpleNamespace(verified=True, findings=())

    async def save(*_args, **_kwargs):
        return ArtifactSaved(
            status="saved",
            artifact_id=19,
            generation=1,
            title="Quarterly update",
            format="video",
            files=[],
        )

    monkeypatch.setattr(executor, "get_registry", get_registry)
    monkeypatch.setattr(executor, "_heartbeat", noop)
    monkeypatch.setattr(executor, "_author_video", author)
    monkeypatch.setattr(executor, "synthesize_narration", narration)
    monkeypatch.setattr(executor, "prepare_video_project", prepare)
    monkeypatch.setattr(executor, "_preflight_and_review", noop)
    monkeypatch.setattr(executor, "_render", frames_render)
    monkeypatch.setattr(executor, "assemble_video_from_stills", stills_assembly)
    monkeypatch.setattr(executor, "verify_artifact", verify)
    monkeypatch.setattr(executor, "_save_verified", save)
    monkeypatch.setattr(executor, "get_vision_llm", noop)
    monkeypatch.setattr(executor, "probe_sandbox_duration", noop)
    return taken


async def _run(monkeypatch, mode: str):
    monkeypatch.setattr(executor.app_config, "VIDEO_RENDER_MODE", mode)
    session = SimpleNamespace(commit=AsyncMock(), rollback=AsyncMock())
    return await executor.execute_video_deliverable(session, _job(), object())


async def test_the_unset_default_still_renders_every_frame(
    monkeypatch, pipeline
) -> None:
    await _run(monkeypatch, "frames")
    assert pipeline == ["frames"]


async def test_stills_assembles_from_scene_parts_instead(monkeypatch, pipeline) -> None:
    await _run(monkeypatch, "stills")
    assert pipeline == ["stills"]


async def test_an_unknown_mode_fails_before_anything_is_authored(
    monkeypatch, pipeline
) -> None:
    authored: list[str] = []

    async def author(*_args):
        authored.append("author")
        return _authored()

    monkeypatch.setattr(executor, "_author_video", author)
    with pytest.raises(ValueError, match="VIDEO_RENDER_MODE"):
        await _run(monkeypatch, "hybrid")
    assert authored == []
    assert pipeline == []
