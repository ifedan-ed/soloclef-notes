from __future__ import annotations

import json
from types import SimpleNamespace

import pytest

from app.artifacts.verification import vision
from app.services.billable_calls import QuotaInsufficientError
from app.utils.structured_output import StructuredOutputError


async def test_review_checks_small_document_in_one_contextual_call(monkeypatch):
    calls = []

    async def fake_invoke_json(_llm, messages, _model, **_kwargs):
        calls.append(messages)
        return vision.VisionVerdict()

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)
    paths = ("/tmp/page-1.jpg", "/tmp/page-2.jpg")

    result = await vision.review_pages(
        SimpleNamespace(),
        tuple((path, b"jpeg") for path in paths),
    )

    assert result.clean
    assert len(calls) == 1
    assert "flowing document" in calls[0][0].content[0]["text"]


async def test_review_uses_slide_framing_with_shared_verdict_contract(monkeypatch):
    prompts = []

    async def fake_invoke_json(_llm, messages, _model, **_kwargs):
        prompts.append(messages[0].content[0]["text"])
        return vision.VisionVerdict()

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)
    image = (("/tmp/page-1.jpg", b"jpeg"),)

    await vision.review_pages(SimpleNamespace(), image)
    await vision.review_pages(SimpleNamespace(), image, review_kind="slides")

    assert "one flowing document" in prompts[0]
    assert "Each slide is self-contained" in prompts[1]
    assert prompts[0].endswith(vision.VERDICT_INSTRUCTIONS)
    assert prompts[1].endswith(vision.VERDICT_INSTRUCTIONS)


async def test_review_returns_blocking_findings(monkeypatch):
    async def fake_invoke_json(_llm, _messages, _model, **_kwargs):
        return vision.VisionVerdict(blocking_findings=["Footer is clipped"])

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)

    result = await vision.review_pages(
        SimpleNamespace(),
        (("/tmp/page-1.jpg", b"jpeg"),),
    )

    assert not result.clean
    assert result.findings == ("Footer is clipped",)


async def test_review_keeps_warnings_non_blocking(monkeypatch):
    async def fake_invoke_json(_llm, _messages, _model, **_kwargs):
        return vision.VisionVerdict(warnings=["Final page has generous whitespace"])

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)

    result = await vision.review_pages(
        SimpleNamespace(),
        (("/tmp/page-1.jpg", b"jpeg"),),
    )

    assert result.clean
    assert not result.findings
    assert result.warnings == ("Final page has generous whitespace",)


async def test_review_preserves_quota_unavailable_reason(monkeypatch):
    async def fake_invoke_json(_llm, _messages, _model, **_kwargs):
        raise QuotaInsufficientError(
            usage_type="artifact_verification",
            balance_micros=0,
            remaining_micros=0,
        )

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)

    result = await vision.review_pages(
        SimpleNamespace(),
        (("/tmp/page-1.jpg", b"jpeg"),),
    )

    assert not result.clean
    assert not result.findings
    assert "credit is insufficient" in (result.unavailable_reason or "")


async def test_known_defect_takes_precedence_over_quota_failure(monkeypatch):
    calls = 0

    async def fake_invoke_json(_llm, _messages, _model, **_kwargs):
        nonlocal calls
        calls += 1
        if calls == 1:
            raise QuotaInsufficientError(
                usage_type="artifact_verification",
                balance_micros=0,
                remaining_micros=0,
            )
        return vision.VisionVerdict(blocking_findings=["Footer is clipped"])

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)
    paths = tuple(f"/tmp/page-{number}.jpg" for number in range(1, 22))

    result = await vision.review_pages(
        SimpleNamespace(),
        tuple((path, b"jpeg") for path in paths),
    )

    assert not result.clean
    assert result.unavailable_reason is None
    assert "Footer is clipped" in result.findings


async def test_long_document_uses_overlapping_windows(monkeypatch):
    calls = []

    async def fake_invoke_json(_llm, messages, _model, **_kwargs):
        calls.append(messages)
        return vision.VisionVerdict()

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)
    paths = tuple(f"/tmp/page-{number}.jpg" for number in range(1, 26))

    result = await vision.review_pages(
        SimpleNamespace(),
        tuple((path, b"jpeg") for path in paths),
    )

    assert result.clean
    assert len(calls) == 2


@pytest.mark.parametrize(
    "failure",
    [
        StructuredOutputError("could not parse VisionVerdict after one retry"),
        TimeoutError(),
    ],
    ids=["unreadable reply", "timeout"],
)
async def test_an_unreadable_review_is_unavailable_not_a_defect(monkeypatch, failure):
    async def fake_invoke_json(_llm, _messages, _model, **_kwargs):
        raise failure

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)

    result = await vision.review_pages(
        SimpleNamespace(),
        (("/tmp/page-1.jpg", b"jpeg"),),
    )

    assert not result.clean
    assert result.findings == ()
    assert type(failure).__name__ in (result.unavailable_reason or "")


async def test_each_vision_attempt_gets_the_full_timeout(monkeypatch):
    seen = {}

    async def fake_invoke_json(_llm, _messages, _model, **kwargs):
        seen.update(kwargs)
        return vision.VisionVerdict()

    monkeypatch.setattr(vision, "invoke_json", fake_invoke_json)

    await vision.review_pages(SimpleNamespace(), (("/tmp/page-1.jpg", b"jpeg"),))

    assert seen == {"attempt_timeout": vision.VISION_TIMEOUT_SECONDS}


class _Reply:
    def __init__(self, content):
        self.content = content


class _ScriptedLLM:
    def __init__(self, *replies):
        self._replies = list(replies)
        self.calls = 0

    async def ainvoke(self, _messages):
        self.calls += 1
        return _Reply(self._replies.pop(0))


@pytest.mark.parametrize(
    ("reply", "findings", "warnings"),
    [
        (
            {"blocking_findings": [{"issue": "Footer clipped", "page": 3}]},
            ("issue: Footer clipped; page: 3",),
            (),
        ),
        (
            {"blocking_findings": "Footer clipped", "warnings": None},
            ("Footer clipped",),
            (),
        ),
        (
            {"warnings": ["Tight margin", {"note": "low contrast", "slide": 2}]},
            (),
            ("Tight margin", "note: low contrast; slide: 2"),
        ),
    ],
    ids=["object finding", "string instead of list", "mixed warnings"],
)
async def test_malformed_vision_verdicts_are_read_on_the_first_ask(
    reply, findings, warnings
):
    llm = _ScriptedLLM(json.dumps(reply))

    result = await vision.review_pages(llm, (("/tmp/page-1.jpg", b"jpeg"),))

    assert llm.calls == 1
    assert result.findings == findings
    assert result.warnings == warnings
    assert result.unavailable_reason is None


async def test_a_vision_reply_unreadable_twice_is_unavailable():
    llm = _ScriptedLLM("Looks fine to me.", "Honestly, it looks fine.")

    result = await vision.review_pages(llm, (("/tmp/page-1.jpg", b"jpeg"),))

    assert llm.calls == 2
    assert result.findings == ()
    assert "could not read" in (result.unavailable_reason or "")
