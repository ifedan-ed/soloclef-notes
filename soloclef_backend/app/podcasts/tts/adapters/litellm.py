"""Hosted TTS through a proxy, in one ``aspeech`` call per segment.

Every hosted provider is reached the same way: the model string names it
(``openai/tts-1``, ``openai/openrouter-kokoro-82m-tts``, ``vertex_ai/...``)
and the voice reference is whatever that provider expects, which the catalog
already supplies. Which proxy serves the request is not a fact this adapter
knows: the address is ``TTS_SERVICE_API_BASE``, so moving from one proxy to
another is that setting and nothing here.

The deadline and the number of attempts are configured rather than left to the
client library, because some of what the proxy forwards to can stop answering
altogether. See :mod:`app.podcasts.tts.attempts`.
"""

from __future__ import annotations

import asyncio

from ..attempts import (
    AttemptPolicy,
    TransientSynthesisError,
    load_attempt_policy,
    with_attempts,
)
from ..audio import SynthesizedAudio
from ..decoded_length import MINIMUM_AUDIBLE_SECONDS, audio_seconds
from ..errors import TextToSpeechError
from ..port import TextToSpeech
from ..request import SynthesisRequest


def takes_instructions(model: str) -> bool:
    """gpt-4o-mini-tts takes a speaking-style instruction; tts-1(-hd) refuses one."""
    return "4o-mini-tts" in model.lower()


# Segments are always MP3: the renderer, narration and previews name their
# files from ``container`` before any audio exists. Most hosted providers send
# MP3; any that do not are transcoded here (see ``as_mp3``).
_CONTAINER = "mp3"

# Grok Voice TTS (through OpenRouter) answers an MP3 request with headerless
# 16-bit mono PCM at 24 kHz, which FFmpeg cannot merge as MP3 ("Header
# missing"). Bytes in no container FFmpeg can identify are read as that.
_RAW_PCM_ARGS = ("-f", "s16le", "-ar", "24000", "-ac", "1")
_MP3_BITRATE = "128k"


def is_mp3(data: bytes) -> bool:
    """An ID3 tag or an MPEG audio frame sync at the start."""
    return data[:3] == b"ID3" or (
        len(data) > 1 and data[0] == 0xFF and (data[1] & 0xE0) == 0xE0
    )


def _has_container(data: bytes) -> bool:
    """A container FFmpeg identifies on its own: WAV, Ogg, FLAC, MP4/AAC, WebM."""
    head = data[:12]
    return (
        head[:4] in (b"RIFF", b"OggS", b"fLaC", b"\x1aE\xdf\xa3")
        or head[4:8] == b"ftyp"
        or (len(head) > 1 and head[0] == 0xFF and (head[1] & 0xF6) == 0xF0)
    )


async def as_mp3(data: bytes) -> bytes:
    """``data`` as MP3, transcoding anything else (raw PCM included)."""
    if is_mp3(data):
        return data
    source = () if _has_container(data) else _RAW_PCM_ARGS
    process = await asyncio.create_subprocess_exec(
        "ffmpeg",
        "-hide_banner",
        "-loglevel",
        "error",
        *source,
        "-i",
        "pipe:0",
        "-ac",
        "1",
        "-b:a",
        _MP3_BITRATE,
        "-f",
        "mp3",
        "pipe:1",
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    out, err = await process.communicate(data)
    if process.returncode != 0 or not out:
        raise TextToSpeechError(
            "could not convert the synthesised audio to MP3: "
            + err.decode(errors="replace")[:300]
        )
    return out


class LiteLlmTextToSpeech(TextToSpeech):
    """Synthesises segments through any hosted TTS model the proxy serves.

    The name is kept for continuity with the deployments that configured it;
    nothing in here depends on which proxy answers.
    """

    def __init__(
        self,
        *,
        model: str,
        api_base: str | None = None,
        api_key: str | None = None,
        attempts: AttemptPolicy | None = None,
    ) -> None:
        self._model = model
        self._api_base = api_base
        self._api_key = api_key
        self._attempts = attempts or load_attempt_policy()

    @property
    def container(self) -> str:
        return _CONTAINER

    async def synthesize(self, request: SynthesisRequest) -> SynthesizedAudio:
        import time

        from litellm import aspeech

        from app.observability.domains import speech

        kwargs = {
            "model": self._model,
            "voice": request.voice,
            "input": request.text,
            # Attempts are counted here, not inside the client, so the cap and
            # the deadline are the two configured numbers and nothing else.
            "max_retries": 0,
            "timeout": self._attempts.timeout,
        }
        if self._api_base:
            kwargs["api_base"] = self._api_base
        if self._api_key:
            kwargs["api_key"] = self._api_key
        if request.instructions and takes_instructions(self._model):
            kwargs["instructions"] = request.instructions

        t0 = time.perf_counter()
        with speech.synthesis_span(provider="litellm", model=self._model):
            try:
                data = await with_attempts(
                    self._attempts,
                    lambda: _audible_mp3(aspeech, kwargs, self._model),
                    describe=f"{self._model} synthesis",
                )
                return SynthesizedAudio(data=data, container=_CONTAINER)
            finally:
                speech.record_synthesis_duration(
                    (time.perf_counter() - t0) * 1000,
                    provider="litellm",
                    model=self._model,
                )


async def _audible_mp3(aspeech, kwargs: dict, model: str) -> bytes:
    """One attempt: an MP3 that decodes to speech, or an error.

    The length is checked, not just the presence of bytes, because an answer
    can be well-formed and still be silence: a transfer that stopped partway
    leaves a short MP3, and a proxy's error page read as raw samples decodes
    to a few milliseconds of noise. Both are retried as transfer faults.
    """
    response = await aspeech(**kwargs)
    data = getattr(response, "content", None)
    if not data:
        raise TransientSynthesisError(f"{model} returned no audio")

    seconds = await audio_seconds(
        data, headerless_pcm=not (is_mp3(data) or _has_container(data))
    )
    if seconds < MINIMUM_AUDIBLE_SECONDS:
        raise TransientSynthesisError(
            f"{model} returned {len(data)} bytes holding {seconds:.3f}s of "
            "audio, which is not a spoken segment"
        )
    return await as_mp3(data)
