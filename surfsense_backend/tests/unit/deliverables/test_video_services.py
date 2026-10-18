from __future__ import annotations

import asyncio
import json
from pathlib import PurePosixPath

import pytest

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools import (
    prepare_video_project as prepare_service,
    review_video_stills as review_service,
)
from tests.utils.fake_sandbox import FakeSandboxSession

pytestmark = pytest.mark.unit

WORKDIR = PurePosixPath("/workspace/deliverable-job-7")


def _video_scene(number: int) -> prepare_service.VideoScene:
    return prepare_service.VideoScene(
        slide_number=number,
        filename=f"scene-{number}.tsx",
        code=(
            'import React from "react";\n'
            f"const Scene{number} = () => <div>{number}</div>;\n"
            f"export default Scene{number};\n"
        ),
        audio=f"slide-{number}.wav",
    )


async def test_prepare_video_project_writes_typed_json() -> None:
    session = FakeSandboxSession({})
    scene = _video_scene(1)

    result = await prepare_service.prepare_video_project(
        prepare_service.VideoProject(scenes=[scene]),
        session=session,
        workdir=WORKDIR,
    )

    assert result["props_path"] == f"{WORKDIR}/props.json"
    payload = json.loads(session.writes[result["props_path"]])
    assert payload["scenes"][0]["code"] == scene.code
    assert payload["scenes"][0]["filename"] == "scene-1.tsx"
    assert session.commands == []


@pytest.mark.parametrize("scene_count", [20, 21])
async def test_prepare_video_project_enforces_scene_policy(scene_count) -> None:
    if scene_count == 21:
        with pytest.raises(ValueError, match="at most 20 items"):
            prepare_service.VideoProject(
                scenes=[_video_scene(number) for number in range(1, 22)]
            )
        return

    result = await prepare_service.prepare_video_project(
        prepare_service.VideoProject(
            scenes=[_video_scene(number) for number in range(1, scene_count + 1)]
        ),
        session=FakeSandboxSession({}),
        workdir=WORKDIR,
    )
    assert result["scene_count"] == 20


async def test_review_video_stills_rejects_traversal_before_sandbox_access() -> None:
    with pytest.raises(ValueError, match="relative"):
        await review_service.review_video_stills(
            ["../other-job/frame.jpg"],
            session=FakeSandboxSession({}),
            vision_llm=object(),
            workdir=WORKDIR,
        )


async def test_review_video_stills_is_bounded(monkeypatch) -> None:
    still = f"{WORKDIR}/stills/frame.jpg"
    session = FakeSandboxSession({still: b"jpeg"})
    vision_llm = object()
    criterion = review_service.VideoCriterionReview(verdict="pass")
    verdict = review_service.VideoStillReview(
        clipping=criterion,
        overflow=criterion,
        contrast=criterion,
        hierarchy=criterion,
        blank_frames=criterion,
        safe_margins=criterion,
        summary="All sampled frames pass.",
    )

    async def invoke_json(llm, messages, schema, **_kwargs):
        assert llm is vision_llm
        assert schema is review_service.VideoStillReview
        assert len(messages[0].content) == 3
        return verdict

    monkeypatch.setattr(review_service, "invoke_json", invoke_json)

    result = await review_service.review_video_stills(
        ["stills/frame.jpg"],
        session=session,
        vision_llm=vision_llm,
        workdir=WORKDIR,
    )

    assert result["status"] == "reviewed"
    assert set(result["review"]) == {
        "clipping",
        "overflow",
        "contrast",
        "hierarchy",
        "blank_frames",
        "safe_margins",
        "summary",
    }


async def test_review_video_stills_fails_safely_without_vision() -> None:
    still = f"{WORKDIR}/frame.jpg"

    result = await review_service.review_video_stills(
        ["frame.jpg"],
        session=FakeSandboxSession({still: b"jpeg"}),
        vision_llm=None,
        workdir=WORKDIR,
    )

    assert result == {
        "status": "unavailable",
        "reason": "No vision-capable model is configured for this workspace",
    }


def test_review_video_stills_normalizes_observed_model_field_names() -> None:
    criterion = {"status": "warning", "evidence": ["frame is close to edge"]}
    review = review_service.VideoStillReview.model_validate(
        {
            "clipping": criterion,
            "overflow": criterion,
            "contrast": criterion,
            "visualHierarchy": criterion,
            "blankFrames": criterion,
            "safeMargins": criterion,
            "summary": {
                "status": "warning",
                "evidence": ["Minor warnings.", "Improve edge spacing."],
            },
        }
    )

    assert review.hierarchy.verdict == "warning"
    assert review.blank_frames.evidence == ["frame is close to edge"]
    assert review.safe_margins.verdict == "warning"
    assert review.summary == "Minor warnings.; Improve edge spacing."


def test_review_video_stills_schema_trims_surplus_evidence() -> None:
    """A fourth finding is dropped rather than costing the six verdicts."""
    review = review_service.VideoCriterionReview(
        verdict="blocking",
        evidence=["one", "two", "three", "four"],
    )

    assert review.verdict == "blocking"
    assert review.evidence == ["one", "two", "three"]


def _png(width: int, height: int) -> bytes:
    import io

    from PIL import Image

    out = io.BytesIO()
    Image.new("RGBA", (width, height), (10, 20, 30, 255)).save(out, format="PNG")
    return out.getvalue()


def _decoded_size(data_url: str) -> tuple[int, int]:
    import base64
    import io

    from PIL import Image

    raw = base64.b64decode(data_url.split(",", 1)[1])
    with Image.open(io.BytesIO(raw)) as image:
        return image.size


async def test_review_video_stills_downscales_stills_over_the_provider_limit(
    monkeypatch,
) -> None:
    """Job 7 failed because Anthropic refuses images over 2000 px in a
    many-image request and a render produced a 2576 px still."""
    big = f"{WORKDIR}/stills/big.png"
    small = f"{WORKDIR}/stills/small.png"
    small_bytes = _png(1280, 720)
    session = FakeSandboxSession({big: _png(2576, 1449), small: small_bytes})
    sent: list[str] = []

    async def invoke_json(_llm, messages, _schema, **_kwargs):
        sent.extend(
            part["image_url"]["url"]
            for part in messages[0].content
            if part["type"] == "image_url"
        )
        criterion = review_service.VideoCriterionReview(verdict="pass")
        return review_service.VideoStillReview(
            clipping=criterion,
            overflow=criterion,
            contrast=criterion,
            hierarchy=criterion,
            blank_frames=criterion,
            safe_margins=criterion,
            summary="ok",
        )

    monkeypatch.setattr(review_service, "invoke_json", invoke_json)

    await review_service.review_video_stills(
        ["stills/big.png", "stills/small.png"],
        session=session,
        vision_llm=object(),
        workdir=WORKDIR,
    )

    big_url, small_url = sent
    assert big_url.startswith("data:image/jpeg;base64,")
    width, height = _decoded_size(big_url)
    assert max(width, height) == review_service._MAX_REVIEW_EDGE_PX
    assert abs(width / height - 2576 / 1449) < 0.01
    # A still already within the limit is sent byte for byte.
    assert small_url == "data:image/png;base64," + __import__("base64").b64encode(
        small_bytes
    ).decode("ascii")


def test_review_edge_is_under_the_many_image_ceiling() -> None:
    assert review_service._MAX_REVIEW_EDGE_PX < 2000


class _Reply:
    def __init__(self, content: str) -> None:
        self.content = content


class _ScriptedVisionLLM:
    """Stands in for the vision model: answers from a script, never paid."""

    def __init__(self, *replies: str, hang: bool = False) -> None:
        self._replies = list(replies)
        self._hang = hang
        self.calls: list[list] = []

    async def ainvoke(self, messages):
        self.calls.append(list(messages))
        if self._hang:
            await asyncio.sleep(3600)
        return _Reply(self._replies.pop(0))


async def _review_with(llm) -> dict:
    still = f"{WORKDIR}/stills/frame.jpg"
    return await review_service.review_video_stills(
        ["stills/frame.jpg"],
        session=FakeSandboxSession({still: b"jpeg"}),
        vision_llm=llm,
        workdir=WORKDIR,
    )


async def test_a_hung_still_review_is_advisory_not_fatal(monkeypatch) -> None:
    monkeypatch.setattr(review_service, "_VISION_TIMEOUT_SECONDS", 0.05)
    llm = _ScriptedVisionLLM(hang=True)

    result = await _review_with(llm)

    assert result["status"] == "unavailable"
    assert "TimeoutError" in result["reason"]
    assert len(llm.calls) == 1


async def test_an_unreadable_still_review_is_advisory_after_one_retry() -> None:
    llm = _ScriptedVisionLLM("The slides look fine.", "Still fine, honestly.")

    result = await _review_with(llm)

    assert result["status"] == "unavailable"
    assert "could not be read" in result["reason"]
    assert len(llm.calls) == 2


def _criteria(**overrides) -> dict:
    body = {
        name: {"verdict": "pass", "evidence": []} for name in review_service._CRITERIA
    }
    body.update(overrides)
    return body


def _check_long_summary(review: dict) -> None:
    assert len(review["summary"]) == review_service._MAX_SUMMARY_CHARS
    assert review["summary"].startswith("Educational presentation")


# Every reply is what a vision model could put on the wire; the first two cost
# real renders. Each one must come back "reviewed" on the first ask, with its
# verdicts intact.
_MALFORMED_REPLIES = [
    (
        "observed: a 520-character summary",
        {**_criteria(), "summary": "Educational presentation reviewed. " + "E" * 485},
        _check_long_summary,
    ),
    (
        "observed: summary as {'verdict', 'overall'}",
        {
            **_criteria(),
            "summary": {"verdict": "pass", "overall": "Clear and production-ready."},
        },
        lambda r: r["summary"] == "Clear and production-ready.",
    ),
    (
        "summary as an object with no prose key",
        {**_criteria(), "summary": {"verdict": "pass", "score": 9}},
        lambda r: r["summary"] == "verdict: pass; score: 9",
    ),
    (
        "summary as a list",
        {**_criteria(), "summary": ["Clear.", "Readable."]},
        lambda r: r["summary"] == "Clear.; Readable.",
    ),
    (
        "summary missing",
        _criteria(),
        lambda r: r["summary"] == "",
    ),
    (
        "unknown verdict becomes a noted warning",
        {
            **_criteria(contrast={"verdict": "meh", "evidence": ["dim"]}),
            "summary": "ok",
        },
        lambda r: (
            r["contrast"]["verdict"] == "warning"
            and "'meh'" in r["contrast"]["evidence"][0]
            and r["contrast"]["evidence"][1] == "dim"
        ),
    ),
    (
        "near-miss verdict 'fail' is blocking",
        {**_criteria(clipping={"verdict": "fail", "evidence": []}), "summary": "ok"},
        lambda r: r["clipping"]["verdict"] == "blocking",
    ),
    (
        "four evidence items",
        {
            **_criteria(overflow={"verdict": "warning", "evidence": list("abcd")}),
            "summary": "ok",
        },
        lambda r: r["overflow"]["evidence"] == ["a", "b", "c"],
    ),
    (
        "a 300-character evidence item",
        {
            **_criteria(contrast={"verdict": "warning", "evidence": ["z" * 300]}),
            "summary": "ok",
        },
        lambda r: r["contrast"]["evidence"] == ["z" * 240],
    ),
    (
        "evidence as a string",
        {
            **_criteria(contrast={"verdict": "warning", "evidence": "too dark"}),
            "summary": "ok",
        },
        lambda r: r["contrast"]["evidence"] == ["too dark"],
    ),
    (
        "evidence items as objects",
        {
            **_criteria(
                clipping={
                    "verdict": "warning",
                    "evidence": [{"frame": "scene-02", "issue": "title clipped"}],
                }
            ),
            "summary": "ok",
        },
        lambda r: (
            r["clipping"]["evidence"] == ["frame: scene-02; issue: title clipped"]
        ),
    ),
    (
        "extra keys at both levels",
        {
            **_criteria(
                hierarchy={"verdict": "pass", "evidence": [], "confidence": 0.9}
            ),
            "summary": "ok",
            "recommendations": ["tighten the kerning"],
        },
        lambda r: "recommendations" not in r and r["hierarchy"]["verdict"] == "pass",
    ),
    (
        "a criterion given as a bare verdict",
        {**_criteria(safe_margins="pass"), "summary": "ok"},
        lambda r: r["safe_margins"] == {"verdict": "pass", "evidence": []},
    ),
]


@pytest.mark.parametrize(
    ("reply", "check"),
    [(reply, check) for _, reply, check in _MALFORMED_REPLIES],
    ids=[label for label, _, _ in _MALFORMED_REPLIES],
)
async def test_malformed_still_reviews_are_read_on_the_first_ask(reply, check) -> None:
    llm = _ScriptedVisionLLM(json.dumps(reply))

    result = await _review_with(llm)

    assert result["status"] == "reviewed"
    assert len(llm.calls) == 1
    assert check(result["review"]) is not False


async def test_an_unreadable_still_review_is_retried_with_the_error() -> None:
    good = json.dumps({**_criteria(), "summary": "ok"})
    llm = _ScriptedVisionLLM(json.dumps({"summary": "ok"}), good)

    result = await _review_with(llm)

    assert result["status"] == "reviewed"
    assert "clipping: Field required" in llm.calls[1][-1].content


def test_review_prompt_states_the_schema_limits() -> None:
    prompt = review_service._REVIEW_PROMPT

    assert f"at most {review_service._MAX_EVIDENCE} short" in prompt
    assert f"at most {review_service._MAX_EVIDENCE_CHARS} characters" in prompt
    assert f"ONE plain string of at most {review_service._MAX_SUMMARY_CHARS}" in prompt
    example = json.loads(review_service._EXAMPLE_REPLY)
    assert review_service.VideoStillReview.model_validate(example)
    assert set(example) == {*review_service._CRITERIA, "summary"}
