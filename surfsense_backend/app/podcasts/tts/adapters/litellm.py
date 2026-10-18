"""LiteLLM adapter: hosted TTS (OpenAI, Azure, Vertex AI) via one ``aspeech`` call.

LiteLLM normalises every hosted provider behind the same ``aspeech`` surface,
so a single adapter covers them all. The provider is encoded in the model
string (e.g. ``openai/tts-1``, ``vertex_ai/...``) and the voice reference is
whatever that provider expects, which the catalog already supplies.
"""

from __future__ import annotations

import asyncio

from ..audio import SynthesizedAudio
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


# A long single segment still finishes well under this; retries absorb transient
# upstream failures without failing the whole render.
_TIMEOUT_SECONDS = 600
_MAX_RETRIES = 2


class LiteLlmTextToSpeech(TextToSpeech):
    """Synthesises segments through any LiteLLM-supported hosted TTS model."""

    def __init__(
        self,
        *,
        model: str,
        api_base: str | None = None,
        api_key: str | None = None,
    ) -> None:
        self._model = model
        self._api_base = api_base
        self._api_key = api_key

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
            "max_retries": _MAX_RETRIES,
            "timeout": _TIMEOUT_SECONDS,
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
                try:
                    response = await aspeech(**kwargs)
                except Exception as exc:
                    raise TextToSpeechError(
                        f"{self._model} synthesis failed: {exc}"
                    ) from exc

                data = getattr(response, "content", None)
                if not data:
                    raise TextToSpeechError(f"{self._model} returned no audio")

                return SynthesizedAudio(data=await as_mp3(data), container=_CONTAINER)
            finally:
                speech.record_synthesis_duration(
                    (time.perf_counter() - t0) * 1000,
                    provider="litellm",
                    model=self._model,
                )
