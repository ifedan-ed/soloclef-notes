import json
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from pydantic import ValidationError

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools.synthesize_narration import (
    NarrationTooLongError,
)
from app.artifacts.service import ArtifactSaved
from app.db import DeliverableFailureCode
from app.deliverables.video import executor
from app.deliverables.video.narration_budget import (
    NARRATION_BUDGET_SAFETY_FACTOR,
    NARRATION_WORDS_PER_SECOND,
    narration_word_budget,
    overrun_word_target,
)
from app.tasks.celery_tasks.deliverable_job_tasks import classify_deliverable_failure
from tests.utils.fake_sandbox import FakeSandboxSession


def _scene(number: int = 1) -> executor.AuthoredVideoScene:
    return executor.AuthoredVideoScene(
        slide_number=number,
        filename=f"scene-{number}.tsx",
        on_screen_markdown=f"Scene {number}",
        transcript=f"Narration {number}",
        code=(
            'import React from "react";\n'
            f"const Scene{number}=()=> <div>{number}</div>;\n"
            f"export default Scene{number};"
        ),
    )


def _authored(count: int = 1) -> executor.AuthoredVideo:
    return executor.AuthoredVideo(scenes=[_scene(i) for i in range(1, count + 1)])


def _job(**overrides):
    values = {
        "id": 7,
        "kind": "video",
        "title": "Quarterly update",
        "workspace_id": 3,
        "thread_id": 11,
        "tool_call_id": "tool-1",
        "celery_task_id": "deliverable-job:7:attempt:1",
        "attempt_count": 1,
        "request": {
            "version": 1,
            "brief": "Explain the quarter",
            "source_references": ["/documents/report.pdf"],
            "revision_artifact_id": None,
            "root_thread_id": 11,
        },
    }
    values.update(overrides)
    return SimpleNamespace(**values)


def test_video_request_is_strict_and_bound_to_a_root_thread() -> None:
    request = executor.VideoJobRequestV1.model_validate(_job().request)
    assert request.root_thread_id == 11

    with pytest.raises(ValidationError):
        executor.VideoJobRequestV1.model_validate({**_job().request, "version": 2})
    with pytest.raises(ValidationError):
        executor.VideoJobRequestV1.model_validate(
            {**_job().request, "revision_artifact_id": True}
        )
    with pytest.raises(ValidationError):
        executor.VideoJobRequestV1.model_validate(
            {
                **_job().request,
                "source_references": ["source"] * 26,
            }
        )


def test_authored_video_enforces_twenty_scene_limit() -> None:
    assert len(_authored(20).scenes) == 20
    with pytest.raises(ValidationError, match="at most 20"):
        _authored(21)


def test_creative_draft_is_normalized_to_backend_owned_scene_identity() -> None:
    draft = executor._CreativeVideoDraft.model_validate(
        {
            "title": "Ignored model title",
            "durationSec": 30,
            "language": "en",
            "scenes": [
                {
                    "id": "opening",
                    "durationSec": 10,
                    "narration": "Opening narration",
                    "onScreenMarkdown": "# Opening",
                    "tsx": "export default () => <div>Opening</div>;",
                },
                {
                    "id": "ending",
                    "narration": "Ending narration",
                    "onScreenMarkdown": "# Ending",
                    "tsx_module": "export default () => <div>Ending</div>;",
                },
            ],
        }
    )

    authored = executor._normalize_creative_video(draft)

    assert authored.language == "en"
    assert [(scene.slide_number, scene.filename) for scene in authored.scenes] == [
        (1, "scene-01.tsx"),
        (2, "scene-02.tsx"),
    ]
    assert authored.scenes[0].transcript == "Opening narration"
    assert authored.scenes[0].on_screen_markdown == "# Opening"
    assert authored.scenes[0].code == "export default () => <div>Opening</div>;"


def test_video_repair_preserves_identity_narration_and_language() -> None:
    authored = executor.AuthoredVideo(language="en", scenes=[_scene(1), _scene(2)])
    repair = executor._VideoRepairDraft.model_validate(
        {
            "language": "fr",
            "scenes": [
                {
                    "slide_number": 9,
                    "filename": "changed.tsx",
                    "narration": "Changed narration",
                    "onScreenMarkdown": "Repaired one",
                    "tsx": "export default () => <div>Repaired one</div>;",
                },
                {
                    "on_screen_markdown": "Repaired two",
                    "code": "export default () => <div>Repaired two</div>;",
                },
            ],
        }
    )

    repaired = executor._merge_video_repair(authored, repair)

    assert repaired.language == "en"
    assert [
        (scene.slide_number, scene.filename, scene.transcript)
        for scene in repaired.scenes
    ] == [
        (scene.slide_number, scene.filename, scene.transcript)
        for scene in authored.scenes
    ]
    assert [scene.on_screen_markdown for scene in repaired.scenes] == [
        "Repaired one",
        "Repaired two",
    ]
    assert repaired.scenes[0].code == ("export default () => <div>Repaired one</div>;")


def test_video_repair_rejects_changed_scene_count() -> None:
    repair = executor._VideoRepairDraft.model_validate(
        {
            "scenes": [
                {
                    "on_screen_markdown": "Only one",
                    "code": "export default () => <div>Only one</div>;",
                }
            ]
        }
    )

    with pytest.raises(ValueError, match="changed scene count"):
        executor._merge_video_repair(_authored(2), repair)


async def test_executor_runs_explicit_stages_and_owns_sandbox(monkeypatch) -> None:
    stages: list[str] = []
    heartbeats: list[tuple[str, int]] = []
    sandbox = FakeSandboxSession()
    session = SimpleNamespace(commit=AsyncMock(), rollback=AsyncMock())

    class Registry:
        async def get_session(self, owner_id, workspace_id):
            assert (owner_id, workspace_id) == ("deliverable-job-7-attempt-1", 3)
            return sandbox

    async def get_registry():
        return Registry()

    async def author(*_args):
        stages.append("author")
        return _authored()

    async def narration(*_args, **_kwargs):
        stages.append("narration")
        return [{"slide_number": 1, "audio": "slide-1.wav", "duration_seconds": 5}]

    async def prepare(*_args, **_kwargs):
        stages.append("prepare")
        return {"props_path": "/workspace/deliverable-job-7/props.json"}

    async def preflight(*_args, **_kwargs):
        stages.extend(("preflight", "stills", "review"))
        return None

    async def render(*_args, **_kwargs):
        stages.append("render")

    async def verify(*_args, **_kwargs):
        stages.append("verify")
        return SimpleNamespace(verified=True, findings=())

    async def save(*_args, **_kwargs):
        stages.append("save")
        return ArtifactSaved(
            status="saved",
            artifact_id=19,
            generation=1,
            title="Quarterly update",
            format="video",
            files=[],
        )

    async def vision(*_args, **_kwargs):
        return None

    async def heartbeat(_session, job_id, *, phase, progress, task_id):
        assert job_id == 7
        assert task_id == "deliverable-job:7:attempt:1"
        heartbeats.append((phase, progress))
        return SimpleNamespace(id=job_id)

    monkeypatch.setattr(executor, "get_registry", get_registry)
    monkeypatch.setattr(executor, "heartbeat_deliverable_job", heartbeat)
    monkeypatch.setattr(executor, "_author_video", author)
    monkeypatch.setattr(executor, "synthesize_narration", narration)
    monkeypatch.setattr(executor, "prepare_video_project", prepare)
    monkeypatch.setattr(executor, "_preflight_and_review", preflight)
    monkeypatch.setattr(executor, "_render", render)
    monkeypatch.setattr(executor, "verify_artifact", verify)
    monkeypatch.setattr(executor, "_save_verified", save)
    monkeypatch.setattr(executor, "get_vision_llm", vision)

    result = await executor.execute_video_deliverable(session, _job(), object())

    assert stages == [
        "author",
        "narration",
        "prepare",
        "preflight",
        "stills",
        "review",
        "render",
        "verify",
        "save",
    ]
    assert heartbeats == [
        ("preparing", 5),
        ("authoring", 10),
        ("narrating", 25),
        ("preparing", 40),
        ("reviewing", 50),
        ("rendering", 65),
        ("verifying", 85),
        ("saving", 95),
    ]
    assert all(0 <= progress < 100 for _, progress in heartbeats)
    assert session.commit.await_count == len(heartbeats)
    assert result.artifact_id == 19
    assert result.duration_seconds == 5


class _ShorteningLLM:
    """Answers narration-shortening prompts with fixed transcripts."""

    def __init__(self, *replies: list[str]) -> None:
        self.replies = list(replies)
        self.requests: list[dict] = []

    async def ainvoke(self, messages):
        assert "Shorten the narration" in messages[0].content
        self.requests.append(json.loads(messages[1].content))
        transcripts = self.replies.pop(0)
        return SimpleNamespace(
            content=json.dumps(
                {"scenes": [{"transcript": text} for text in transcripts]}
            )
        )


def _patch_through_render(monkeypatch, narration, author=None):
    """Stub the sandbox stages so a test drives authoring and narration only."""
    sandbox = FakeSandboxSession()

    class Registry:
        async def get_session(self, *_args):
            return sandbox

    async def get_registry():
        return Registry()

    async def default_author(*_args):
        return _authored()

    async def prepare(*_args, **_kwargs):
        return {"props_path": "/workspace/deliverable-job-7/props.json"}

    async def noop(*_args, **_kwargs):
        return None

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
    monkeypatch.setattr(executor, "_author_video", author or default_author)
    monkeypatch.setattr(executor, "synthesize_narration", narration)
    monkeypatch.setattr(executor, "prepare_video_project", prepare)
    monkeypatch.setattr(executor, "_preflight_and_review", noop)
    monkeypatch.setattr(executor, "_render", noop)
    monkeypatch.setattr(executor, "verify_artifact", verify)
    monkeypatch.setattr(executor, "_save_verified", save)
    monkeypatch.setattr(executor, "get_vision_llm", noop)


def test_author_prompt_states_a_word_budget_derived_from_the_duration_limit() -> None:
    budget = narration_word_budget()
    assert budget == int(
        executor.VIDEO_SPEC.max_duration_seconds
        * NARRATION_WORDS_PER_SECOND
        * NARRATION_BUDGET_SAFETY_FACTOR
    )
    assert f"under {budget} words" in executor._AUTHOR_PROMPT
    assert "__MAX" not in executor._AUTHOR_PROMPT


def test_overrun_word_target_scales_to_just_under_the_limit() -> None:
    # The real 216.967 s overrun against a 180 s cap needs about a 21% cut.
    assert overrun_word_target(500, 216.967, 180) == 394


def test_narration_shortening_preserves_identity_code_and_markdown() -> None:
    authored = executor.AuthoredVideo(language="en", scenes=[_scene(1), _scene(2)])
    shortened = executor._NarrationShorteningDraft.model_validate(
        {
            "scenes": [
                {"transcript": "Short one", "code": "evil", "slide_number": 9},
                {"narration": "Short two"},
            ]
        }
    )

    merged = executor._merge_shortened_narration(authored, shortened)

    assert [scene.transcript for scene in merged.scenes] == ["Short one", "Short two"]
    assert [
        (s.slide_number, s.filename, s.code, s.on_screen_markdown)
        for s in merged.scenes
    ] == [
        (s.slide_number, s.filename, s.code, s.on_screen_markdown)
        for s in authored.scenes
    ]
    with pytest.raises(ValueError, match="changed scene count"):
        executor._merge_shortened_narration(
            authored,
            executor._NarrationShorteningDraft.model_validate(
                {"scenes": [{"transcript": "Only one"}]}
            ),
        )


async def test_over_budget_draft_is_shortened_before_any_synthesis(monkeypatch) -> None:
    budget = narration_word_budget()
    long_text = " ".join(["word"] * (budget + 50))
    synthesized: list[list[str]] = []

    async def author(*_args):
        return executor.AuthoredVideo(
            scenes=[_scene(1).model_copy(update={"transcript": long_text})]
        )

    async def narration(slides, *_args, **_kwargs):
        synthesized.append([slide["transcript"] for slide in slides])
        return [{"slide_number": 1, "audio": "slide-1.wav", "duration_seconds": 60}]

    _patch_through_render(monkeypatch, narration, author)
    llm = _ShorteningLLM(["A much shorter narration."])

    result = await executor.execute_video_deliverable(object(), _job(), llm)

    assert synthesized == [["A much shorter narration."]]
    assert llm.requests[0]["current_words"] == budget + 50
    assert llm.requests[0]["target_words"] == budget
    assert result.duration_seconds == 60


async def test_word_check_gives_up_after_bounded_attempts_and_lets_tts_decide(
    monkeypatch,
) -> None:
    long_text = " ".join(["word"] * (narration_word_budget() + 1))
    synthesized: list[str] = []

    async def author(*_args):
        return executor.AuthoredVideo(
            scenes=[_scene(1).model_copy(update={"transcript": long_text})]
        )

    async def narration(slides, *_args, **_kwargs):
        synthesized.append(slides[0]["transcript"])
        return [{"slide_number": 1, "audio": "slide-1.wav", "duration_seconds": 200}]

    _patch_through_render(monkeypatch, narration, author)
    llm = _ShorteningLLM([long_text], [long_text])

    result = await executor.execute_video_deliverable(object(), _job(), llm)

    assert len(llm.requests) == 2
    assert synthesized == [long_text]
    assert result.duration_seconds == 200


async def test_overlong_synthesized_narration_is_shortened_and_succeeds(
    monkeypatch,
) -> None:
    durations = iter((216.967, 170.0))
    synthesized: list[list[str]] = []

    async def narration(slides, *_args, **_kwargs):
        synthesized.append([slide["transcript"] for slide in slides])
        duration = next(durations)
        if duration > executor.VIDEO_SPEC.max_duration_seconds:
            raise NarrationTooLongError(
                duration, executor.VIDEO_SPEC.max_duration_seconds
            )
        return [
            {"slide_number": n, "audio": f"slide-{n}.wav", "duration_seconds": 85.0}
            for n in (1, 2)
        ]

    async def author(*_args):
        return _authored(2)

    _patch_through_render(monkeypatch, narration, author)
    monkeypatch.setattr(
        executor,
        "VIDEO_SPEC",
        executor.VIDEO_SPEC.__class__(
            max_duration_seconds=180,
            max_scenes=12,
            repair_cycles=2,
            soft_time_limit_seconds=3600,
            hard_time_limit_seconds=3900,
        ),
    )
    llm = _ShorteningLLM(["Short one.", "Short two."])

    result = await executor.execute_video_deliverable(object(), _job(), llm)

    assert synthesized == [["Narration 1", "Narration 2"], ["Short one.", "Short two."]]
    request = llm.requests[0]
    assert request["measured_seconds"] == 217.0
    assert request["limit_seconds"] == 180
    assert request["current_words"] == 4
    assert request["target_words"] == overrun_word_target(4, 216.967, 180)
    assert result.duration_seconds == 170.0


async def test_narration_still_too_long_after_shortening_fails_duration_limit(
    monkeypatch,
) -> None:
    calls = 0

    async def narration(*_args, **_kwargs):
        nonlocal calls
        calls += 1
        return [{"slide_number": 1, "audio": "slide-1.wav", "duration_seconds": 600.1}]

    _patch_through_render(monkeypatch, narration)
    llm = _ShorteningLLM(["Still long."])

    with pytest.raises(ValueError, match="600-second limit") as raised:
        await executor.execute_video_deliverable(object(), _job(), llm)

    assert calls == 2
    assert len(llm.requests) == 1
    code, retryable = classify_deliverable_failure(raised.value)
    assert (code, retryable) == (DeliverableFailureCode.DURATION_LIMIT, False)


async def test_executor_stops_after_two_repairs(monkeypatch) -> None:
    sandbox = FakeSandboxSession()
    repairs = 0
    progress_updates = []
    preflights = iter(("compile failed", None, None))

    class Registry:
        async def get_session(self, *_args):
            return sandbox

    async def get_registry():
        return Registry()

    async def author(*_args):
        return _authored()

    async def narration(*_args, **_kwargs):
        return [{"slide_number": 1, "audio": "slide-1.wav", "duration_seconds": 5}]

    async def prepare(*_args, **_kwargs):
        return {"props_path": "/workspace/deliverable-job-7/props.json"}

    async def preflight(*_args, **_kwargs):
        return next(preflights)

    async def repair(_llm, authored, _finding):
        nonlocal repairs
        repairs += 1
        return authored

    async def noop(*_args, **_kwargs):
        return None

    async def failed_verify(*_args, **_kwargs):
        return SimpleNamespace(verified=False, findings=("bad frame",))

    async def heartbeat(_session, _job_id, _phase, progress, **_kwargs):
        progress_updates.append(progress)

    monkeypatch.setattr(executor, "get_registry", get_registry)
    monkeypatch.setattr(executor, "_heartbeat", heartbeat)
    monkeypatch.setattr(executor, "_author_video", author)
    monkeypatch.setattr(executor, "synthesize_narration", narration)
    monkeypatch.setattr(executor, "prepare_video_project", prepare)
    monkeypatch.setattr(executor, "_preflight_and_review", preflight)
    monkeypatch.setattr(executor, "_repair_video", repair)
    monkeypatch.setattr(executor, "_render", noop)
    monkeypatch.setattr(executor, "verify_artifact", failed_verify)
    monkeypatch.setattr(executor, "get_vision_llm", noop)

    with pytest.raises(RuntimeError, match="verification failed"):
        await executor.execute_video_deliverable(object(), _job(), object())
    assert repairs == 2
    assert progress_updates == sorted(progress_updates)


async def test_heartbeat_stops_work_when_cancellation_wins(monkeypatch) -> None:
    session = SimpleNamespace(commit=AsyncMock(), rollback=AsyncMock())
    monkeypatch.setattr(
        executor,
        "heartbeat_deliverable_job",
        AsyncMock(return_value=None),
    )

    with pytest.raises(executor.DeliverableJobCancellationError):
        await executor._heartbeat(session, 7, "rendering", 65)

    session.rollback.assert_awaited_once()
    session.commit.assert_not_awaited()


async def test_save_requires_current_verification_receipt(monkeypatch) -> None:
    saved = False

    async def missing_receipt(*_args, **_kwargs):
        raise ValueError("Verify this file again before presenting it")

    async def save(*_args, **_kwargs):
        nonlocal saved
        saved = True

    monkeypatch.setattr(executor, "read_receipt", missing_receipt)
    monkeypatch.setattr(executor, "save_artifact", save)

    with pytest.raises(ValueError, match="Verify this file"):
        await executor._save_verified(
            object(),
            FakeSandboxSession(),
            job=_job(),
            request=executor.VideoJobRequestV1.model_validate(_job().request),
            authored=_authored(),
            output_path="/workspace/deliverable-job-7.mp4",
        )
    assert saved is False


def _patch_happy_path(monkeypatch, saved_kwargs: dict, *, probed: float | None):
    sandbox = FakeSandboxSession()

    class Registry:
        async def get_session(self, owner_id, workspace_id):
            return sandbox

    async def get_registry():
        return Registry()

    async def author(*_args):
        return _authored()

    async def narration(*_args, **_kwargs):
        return [{"slide_number": 1, "audio": "slide-1.wav", "duration_seconds": 5.04}]

    async def prepare(*_args, **_kwargs):
        return {"props_path": "/workspace/deliverable-job-7/props.json"}

    async def nothing(*_args, **_kwargs):
        return None

    async def verify(*_args, **_kwargs):
        return SimpleNamespace(verified=True, findings=())

    async def save(*_args, **kwargs):
        saved_kwargs.update(kwargs)
        return ArtifactSaved(
            status="saved",
            artifact_id=19,
            generation=1,
            title="Quarterly update",
            format="video",
            files=[],
        )

    async def probe(session, path):
        assert session is sandbox and path.endswith(".mp4")
        return probed

    async def heartbeat(_session, job_id, **_kwargs):
        return SimpleNamespace(id=job_id)

    monkeypatch.setattr(executor, "get_registry", get_registry)
    monkeypatch.setattr(executor, "heartbeat_deliverable_job", heartbeat)
    monkeypatch.setattr(executor, "_author_video", author)
    monkeypatch.setattr(executor, "synthesize_narration", narration)
    monkeypatch.setattr(executor, "prepare_video_project", prepare)
    monkeypatch.setattr(executor, "_preflight_and_review", nothing)
    monkeypatch.setattr(executor, "_render", nothing)
    monkeypatch.setattr(executor, "verify_artifact", verify)
    monkeypatch.setattr(executor, "_save_verified", save)
    monkeypatch.setattr(executor, "get_vision_llm", nothing)
    monkeypatch.setattr(executor, "probe_sandbox_duration", probe)


async def test_saved_video_carries_the_rendered_files_duration(monkeypatch) -> None:
    saved: dict = {}
    _patch_happy_path(monkeypatch, saved, probed=487.3)
    session = SimpleNamespace(commit=AsyncMock(), rollback=AsyncMock())

    await executor.execute_video_deliverable(session, _job(), object())

    assert saved["duration_seconds"] == 487.3


async def test_unreadable_render_falls_back_to_the_narration_length(
    monkeypatch,
) -> None:
    saved: dict = {}
    _patch_happy_path(monkeypatch, saved, probed=None)
    session = SimpleNamespace(commit=AsyncMock(), rollback=AsyncMock())

    await executor.execute_video_deliverable(session, _job(), object())

    assert saved["duration_seconds"] == 5.0


async def test_save_records_the_duration_in_artifact_metadata(monkeypatch) -> None:
    captured: dict = {}

    async def receipt(*_args, **_kwargs):
        return SimpleNamespace(
            format="video",
            primary_path="/workspace/deliverable-job-7.mp4",
            primary_sha256="a" * 64,
            visual="reviewed",
            unavailable_reason=None,
        )

    async def save(_session, **kwargs):
        captured.update(kwargs)

    monkeypatch.setattr(executor, "read_receipt", receipt)
    monkeypatch.setattr(executor, "save_artifact", save)

    await executor._save_verified(
        object(),
        FakeSandboxSession(),
        job=_job(),
        request=executor.VideoJobRequestV1.model_validate(_job().request),
        authored=_authored(),
        output_path="/workspace/deliverable-job-7.mp4",
        duration_seconds=62.4,
    )

    assert captured["extra_metadata"]["duration_seconds"] == 62.4
    assert captured["extra_metadata"]["verification"]["verified"] is True


class _RawReplyLLM:
    """Answers each call with the next raw string; records every conversation."""

    def __init__(self, *replies: str) -> None:
        self.replies = list(replies)
        self.calls: list[list] = []

    async def ainvoke(self, messages):
        self.calls.append(list(messages))
        return SimpleNamespace(content=self.replies.pop(0))


def _repair_reply(count: int) -> str:
    return json.dumps(
        {
            "scenes": [
                {
                    "on_screen_markdown": f"Fixed {n}",
                    "code": f"export default () => <div>Fixed {n}</div>;",
                }
                for n in range(1, count + 1)
            ]
        }
    )


async def test_repair_scene_count_mismatch_is_retried_with_the_count() -> None:
    llm = _RawReplyLLM(_repair_reply(1), _repair_reply(2))

    repaired = await executor._repair_video(llm, _authored(2), "scene 2 overflows")

    assert [scene.on_screen_markdown for scene in repaired.scenes] == [
        "Fixed 1",
        "Fixed 2",
    ]
    assert len(llm.calls) == 2
    assert "expected 2 scenes, got 1" in llm.calls[1][-1].content
    assert json.loads(llm.calls[0][1].content)["scene_count"] == 2


async def test_repair_scene_count_mismatch_twice_is_a_structured_error() -> None:
    llm = _RawReplyLLM(_repair_reply(3), _repair_reply(1))

    with pytest.raises(executor.StructuredOutputError, match="expected 2 scenes"):
        await executor._repair_video(llm, _authored(2), "scene 2 overflows")


async def test_shortening_scene_count_mismatch_is_retried_with_the_count() -> None:
    llm = _RawReplyLLM(
        json.dumps({"scenes": [{"transcript": "Only one."}]}),
        json.dumps({"scenes": [{"transcript": "One."}, {"transcript": "Two."}]}),
    )

    shortened = await executor._shorten_narration(
        llm, _authored(2), {"reason": "too long", "target_words": 2}
    )

    assert [scene.transcript for scene in shortened.scenes] == ["One.", "Two."]
    assert "expected 2 scenes, got 1" in llm.calls[1][-1].content


async def test_unreadable_pre_synthesis_shortening_keeps_the_draft(
    monkeypatch,
) -> None:
    long_text = " ".join(["word"] * (narration_word_budget() + 20))
    synthesized: list[str] = []

    async def author(*_args):
        return executor.AuthoredVideo(
            scenes=[_scene(1).model_copy(update={"transcript": long_text})]
        )

    async def narration(slides, *_args, **_kwargs):
        synthesized.append(slides[0]["transcript"])
        return [{"slide_number": 1, "audio": "slide-1.wav", "duration_seconds": 170}]

    _patch_through_render(monkeypatch, narration, author)
    llm = _RawReplyLLM("I would rather not.", "Still no JSON, sorry.")

    result = await executor.execute_video_deliverable(object(), _job(), llm)

    # One ask plus its one retry, then the draft goes to synthesis unchanged
    # and the measured duration decides.
    assert len(llm.calls) == 2
    assert synthesized == [long_text]
    assert result.duration_seconds == 170
