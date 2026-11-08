"""A speech service named ``local/...`` is refused, naming the setting.

Kokoro (TTS) and Faster-Whisper (STT) used to run inside this process. They are
gone, so the two settings that could name them must stop a start rather than
reach for something else: a deployment still asking for on-box speech would
otherwise get silence (TTS) or an unconfigured transcription (STT).
"""

from __future__ import annotations

import pytest

from app.speech.hosted_only import (
    LocalSpeechServiceError,
    refuse_a_local_speech_service,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("setting", "service"),
    [
        ("TTS_SERVICE", "local/kokoro"),
        ("STT_SERVICE", "local/base"),
        ("STT_SERVICE", "local/large-v3"),
    ],
)
def test_a_local_service_is_refused_naming_the_setting(setting, service):
    with pytest.raises(LocalSpeechServiceError) as refused:
        refuse_a_local_speech_service(setting, service)

    message = str(refused.value)
    assert setting in message
    assert service in message
    assert "proxy" in message


def test_the_refusal_shows_a_served_model_to_use_instead():
    with pytest.raises(LocalSpeechServiceError) as refused:
        refuse_a_local_speech_service("TTS_SERVICE", "local/kokoro")

    assert "openai/openrouter-kokoro-82m-tts" in str(refused.value)


@pytest.mark.parametrize(
    "service",
    ["openai/openrouter-kokoro-82m-tts", "openai/groq-whisper-large-v3", "vertex_ai/x"],
)
def test_a_served_model_passes(service):
    refuse_a_local_speech_service("TTS_SERVICE", service)


@pytest.mark.parametrize("service", [None, "", "   "])
def test_an_unset_setting_is_not_this_check_s_business(service):
    """Unset is refused where the service is used, naming the setting there."""
    refuse_a_local_speech_service("STT_SERVICE", service)


def test_a_model_whose_name_merely_contains_local_is_served_not_local():
    refuse_a_local_speech_service("TTS_SERVICE", "openai/local-kokoro-tts")
