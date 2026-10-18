"""Whatever a hosted model sends, a LiteLLM TTS segment is MP3.

Grok Voice TTS answers an MP3 request with raw 24 kHz PCM, which FFmpeg then
failed to merge; the adapter transcodes anything that is not MP3.
"""

from __future__ import annotations

import math
import shutil
import struct
import subprocess
from types import SimpleNamespace

import pytest

from app.podcasts.tts.adapters import litellm as adapter
from app.podcasts.tts.request import SynthesisRequest

pytestmark = [
    pytest.mark.unit,
    pytest.mark.skipif(shutil.which("ffmpeg") is None, reason="needs ffmpeg"),
]


def _pcm(seconds: float = 0.5, rate: int = 24000) -> bytes:
    frames = int(seconds * rate)
    return b"".join(
        struct.pack("<h", int(8000 * math.sin(2 * math.pi * 440 * i / rate)))
        for i in range(frames)
    )


def _seconds(mp3: bytes) -> float:
    """Decoded length: a piped MP3 has no duration for ffprobe to read."""
    pcm = subprocess.run(
        [
            "ffmpeg",
            "-loglevel",
            "error",
            "-i",
            "pipe:0",
            "-f",
            "s16le",
            "-ar",
            "24000",
            "-ac",
            "1",
            "pipe:1",
        ],
        input=mp3,
        capture_output=True,
        check=True,
    ).stdout
    return len(pcm) / 48000


async def test_raw_pcm_becomes_mp3_of_the_same_length():
    out = await adapter.as_mp3(_pcm(0.5))
    assert adapter.is_mp3(out)
    assert _seconds(out) == pytest.approx(0.5, abs=0.1)


async def test_mp3_passes_through_untouched():
    mp3 = await adapter.as_mp3(_pcm(0.3))
    assert await adapter.as_mp3(mp3) is mp3


async def test_a_wav_is_read_by_its_header_not_as_raw_pcm():
    wav = subprocess.run(
        [
            "ffmpeg",
            "-loglevel",
            "error",
            "-f",
            "s16le",
            "-ar",
            "24000",
            "-ac",
            "1",
            "-i",
            "pipe:0",
            "-f",
            "wav",
            "pipe:1",
        ],
        input=_pcm(0.4),
        capture_output=True,
        check=True,
    ).stdout
    out = await adapter.as_mp3(wav)
    assert adapter.is_mp3(out)
    assert _seconds(out) == pytest.approx(0.4, abs=0.1)


async def test_the_adapter_returns_mp3_for_a_pcm_reply(monkeypatch):
    async def aspeech(**kwargs):
        return SimpleNamespace(content=_pcm(0.3))

    import litellm

    monkeypatch.setattr(litellm, "aspeech", aspeech)
    tts = adapter.LiteLlmTextToSpeech(model="openai/openrouter-grok-voice-tts-1.0")
    audio = await tts.synthesize(
        SynthesisRequest(text="Hello", voice="ara", language="en")
    )
    assert audio.container == "mp3"
    assert adapter.is_mp3(audio.data)
