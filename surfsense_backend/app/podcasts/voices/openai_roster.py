"""Which OpenAI voices the configured TTS model speaks.

gpt-4o-mini-tts has thirteen voices; tts-1 and tts-1-hd have nine; Grok Voice
TTS has five of its own. LiteLLM
lists a model's voices in ``/model/info`` (``model_info.voices``), so the
list is read from there once per process, and a static list by model name is
used when LiteLLM cannot be reached.
"""

from __future__ import annotations

import logging
from functools import lru_cache

import httpx

from .data.openai import GROK_VOICES, OPENAI_VOICES, TTS1_VOICE_NAMES

logger = logging.getLogger(__name__)

_TIMEOUT_SECONDS = 3.0


def static_voice_names(model: str) -> frozenset[str]:
    """The voices a model name is known to take, without asking LiteLLM."""
    name = model.lower()
    if "grok" in name:
        return frozenset(v.native_ref for v in GROK_VOICES)
    if "tts-1" in name and "4o" not in name:
        return TTS1_VOICE_NAMES
    return frozenset(v.native_ref for v in OPENAI_VOICES)


def model_info_voices(
    model: str, api_base: str | None, api_key: str | None
) -> list[str] | None:
    """``model_info.voices`` for ``model`` from a LiteLLM proxy, or None."""
    if not api_base:
        return None
    headers = {"Authorization": f"Bearer {api_key}"} if api_key else {}
    try:
        response = httpx.get(
            f"{api_base.rstrip('/')}/model/info",
            headers=headers,
            timeout=_TIMEOUT_SECONDS,
        )
        response.raise_for_status()
        entries = response.json().get("data") or []
    except (httpx.HTTPError, ValueError):
        logger.info("LiteLLM model info unavailable; using the static voice list")
        return None
    for entry in entries:
        if isinstance(entry, dict) and entry.get("model_name") == model:
            voices = (entry.get("model_info") or {}).get("voices")
            if isinstance(voices, list) and all(isinstance(v, str) for v in voices):
                return voices
    return None


@lru_cache(maxsize=4)
def openai_voice_names(
    service: str, api_base: str | None, api_key: str | None
) -> frozenset[str]:
    """The voices ``service`` (``openai/<model>``) speaks, LiteLLM first."""
    model = service.split("/", 1)[-1]
    listed = model_info_voices(model, api_base, api_key)
    return frozenset(listed) if listed else static_voice_names(model)
