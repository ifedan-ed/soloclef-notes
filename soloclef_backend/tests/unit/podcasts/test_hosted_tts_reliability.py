"""A hosted segment arrives whole and on time, or the job fails.

Production synthesises through a proxy over somebody else's hosted model, and
Kokoro's speech route has been reported to hang for hours, so the failure
behaviour is pinned as closely as the success behaviour: a deadline a render
queue can survive, a visible cap on attempts, and no answer accepted that
carries no audio. These run against a real loopback socket rather than a
patched client, because a deadline and a body that stops mid-transfer are
transport behaviour and a stubbed coroutine cannot show either.
"""

from __future__ import annotations

import shutil
import time

import pytest

from app.podcasts.tts import SynthesisRequest, TextToSpeechError
from app.podcasts.tts.adapters.litellm import LiteLlmTextToSpeech
from app.podcasts.tts.attempts import AttemptPolicy

from .hosted_speech_stub import (
    HostedSpeechStub,
    audio,
    empty,
    hang,
    not_audio,
    speech_mp3,
    status,
    truncated,
)

pytestmark = [
    pytest.mark.unit,
    # The stub serves real encoded audio and the adapter decodes what it gets.
    pytest.mark.skipif(shutil.which("ffmpeg") is None, reason="needs ffmpeg"),
]

MODEL = "openai/openrouter-kokoro-82m-tts"

# Deadlines short enough that a hang fails inside one test, and no backoff so
# the attempt cap is what the test measures.
IMPATIENT = AttemptPolicy(
    request_timeout_seconds=0.75, max_attempts=3, backoff_seconds=0.0
)


def _adapter(stub: HostedSpeechStub, attempts: AttemptPolicy = IMPATIENT):
    return LiteLlmTextToSpeech(
        model=MODEL,
        api_base=stub.api_base,
        api_key="sk-proxy-test",
        attempts=attempts,
    )


def _request(voice: str = "af_heart"):
    return SynthesisRequest(text="One short line.", voice=voice, language="en-US")


async def test_a_segment_is_the_mp3_the_proxy_returned():
    with HostedSpeechStub([audio()]) as stub:
        result = await _adapter(stub).synthesize(_request())

    assert result.data == speech_mp3()
    assert result.container == "mp3"
    call = stub.calls[0]
    assert call.path.endswith("/audio/speech")
    assert call.payload["model"] == "openrouter-kokoro-82m-tts"
    assert call.payload["voice"] == "af_heart"
    assert call.payload["input"] == "One short line."
    assert call.headers["authorization"] == "Bearer sk-proxy-test"


async def test_the_container_is_mp3_so_duration_is_probed_as_before():
    """The video path ffprobes the written file; the extension must stay mp3."""
    with HostedSpeechStub([audio()]) as stub:
        assert _adapter(stub).container == "mp3"


async def test_rate_limiting_that_clears_is_retried_not_surfaced():
    with HostedSpeechStub([status(429, "slow down"), audio()]) as stub:
        result = await _adapter(stub).synthesize(_request())

    assert result.data == speech_mp3()
    assert len(stub.calls) == 2


async def test_rate_limiting_that_never_clears_fails_the_segment():
    with (
        HostedSpeechStub([status(429, "no credits remaining")]) as stub,
        pytest.raises(TextToSpeechError, match="3 attempts"),
    ):
        await _adapter(stub).synthesize(_request())

    assert len(stub.calls) == 3


async def test_a_server_error_fails_after_the_configured_attempts():
    with HostedSpeechStub([status(502, "bad gateway")]) as stub:
        policy = AttemptPolicy(
            request_timeout_seconds=0.75, max_attempts=2, backoff_seconds=0.0
        )
        with pytest.raises(TextToSpeechError, match="2 attempts"):
            await _adapter(stub, policy).synthesize(_request())

    assert len(stub.calls) == 2


async def test_a_rejected_request_is_not_retried():
    """A 400 answers the same every time; spending three calls on it is waste."""
    with (
        HostedSpeechStub([status(400, "unknown voice")]) as stub,
        pytest.raises(TextToSpeechError),
    ):
        await _adapter(stub).synthesize(_request())

    assert len(stub.calls) == 1


async def test_a_hanging_proxy_fails_inside_its_deadline():
    with HostedSpeechStub([hang(30.0)]) as stub:
        started = time.monotonic()
        with pytest.raises(TextToSpeechError):
            await _adapter(stub).synthesize(_request())
        elapsed = time.monotonic() - started

    assert elapsed < 10.0
    assert len(stub.calls) == 3


async def test_an_answer_with_no_audio_is_an_error_not_a_silent_segment():
    with (
        HostedSpeechStub([empty()]) as stub,
        pytest.raises(TextToSpeechError, match="no audio"),
    ):
        await _adapter(stub).synthesize(_request())


async def test_a_truncated_body_is_an_error():
    with HostedSpeechStub([truncated()]) as stub, pytest.raises(TextToSpeechError):
        await _adapter(stub).synthesize(_request())


async def test_a_body_that_is_not_audio_is_an_error():
    with HostedSpeechStub([not_audio()]) as stub, pytest.raises(TextToSpeechError):
        await _adapter(stub).synthesize(_request())


async def test_the_worst_case_for_a_segment_is_the_two_settings():
    """What an operator is choosing when they set the deadline and the cap."""
    policy = AttemptPolicy(
        request_timeout_seconds=120.0, max_attempts=3, backoff_seconds=2.0
    )

    assert policy.worst_case_seconds == 120.0 * 3 + 2.0 * (0 + 1 + 2)
