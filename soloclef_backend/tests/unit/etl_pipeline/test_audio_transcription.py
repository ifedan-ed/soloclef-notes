"""Which transcriber an audio upload reaches, and what an unusable one says.

Transcription used to run Faster-Whisper in this process, and ``STT_SERVICE``
defaulted to ``local/base`` when it was unset -- so a deployment that named no
transcriber silently got an on-box model. Both are gone: every upload is
transcribed by a model the proxy serves, and the two values that used to mean
"on this box" -- ``local/...`` and unset -- now stop the attempt naming the
setting.
"""

from __future__ import annotations

import pytest

from app.etl_pipeline.parsers.audio import transcribe_audio
from app.speech.hosted_only import LocalSpeechServiceError

pytestmark = pytest.mark.unit


@pytest.fixture
def stt_service(monkeypatch):
    def _set(service: str | None) -> None:
        from app.config import config as app_config

        monkeypatch.setattr(app_config, "STT_SERVICE", service)
        monkeypatch.setattr(app_config, "STT_SERVICE_API_BASE", "http://proxy:4000/v1")
        monkeypatch.setattr(app_config, "STT_SERVICE_API_KEY", "sk-proxy-test")

    return _set


@pytest.mark.parametrize("service", ["local/base", "local/large-v3"])
async def test_a_local_transcriber_is_refused_naming_the_setting(
    stt_service, service, tmp_path
):
    stt_service(service)
    audio = tmp_path / "recording.mp3"
    audio.write_bytes(b"\x00" * 16)

    with pytest.raises(LocalSpeechServiceError) as refused:
        await transcribe_audio(str(audio), "recording.mp3")

    assert "STT_SERVICE" in str(refused.value)
    assert service in str(refused.value)


@pytest.mark.parametrize("service", [None, ""])
async def test_an_unset_transcriber_does_not_mean_a_local_one(
    stt_service, service, tmp_path
):
    """Unset used to mean ``local/base``; it now names the setting instead."""
    stt_service(service)
    audio = tmp_path / "recording.mp3"
    audio.write_bytes(b"\x00" * 16)

    with pytest.raises(ValueError, match="STT_SERVICE"):
        await transcribe_audio(str(audio), "recording.mp3")


async def test_a_served_model_is_transcribed_through_the_proxy(
    stt_service, tmp_path, mocker
):
    stt_service("openai/groq-whisper-large-v3")
    audio = tmp_path / "recording.mp3"
    audio.write_bytes(b"\x00" * 16)
    atranscription = mocker.patch(
        "app.etl_pipeline.parsers.audio.atranscription",
        new=mocker.AsyncMock(return_value={"text": "Hello from audio"}),
    )

    markdown = await transcribe_audio(str(audio), "recording.mp3")

    assert "Hello from audio" in markdown
    kwargs = atranscription.await_args.kwargs
    assert kwargs["model"] == "openai/groq-whisper-large-v3"
    assert kwargs["api_base"] == "http://proxy:4000/v1"
    assert kwargs["api_key"] == "sk-proxy-test"
