"""The voices and speaking style used when nobody chose one.

The owner compared voices on 2026-09-24 and chose gpt-4o-mini-tts with
coral for video narration (the benchmark's best, no misread words) and coral
with ash as podcast hosts. On 2026-09-26 the owner tried Grok Voice TTS
(``openai/openrouter-grok-voice-tts-1.0``; ara, rex) after a medical-script
accuracy test, then chose gpt-4o-mini-tts with marin narrating and marin with
cedar hosting after comparing samples. Grok stays in the catalog; it takes no
speaking-style instruction, so the instructions below only reach
gpt-4o-mini-tts. The choices
are environment settings
(``TTS_NARRATION_VOICE``, ``TTS_PODCAST_VOICES``), in the provider's own voice
names, with a speaking-style instruction for models that take one
(``TTS_NARRATION_INSTRUCTIONS``, ``TTS_PODCAST_INSTRUCTIONS``). A configured
voice the active model lacks is skipped, and the catalog's choice applies.
"""

from __future__ import annotations

from .catalog import VoiceCatalog
from .provider import TtsProvider

# What each provider narrated with before these settings existed, tried after
# the configured voice so decks without one keep the voice they had.
LEGACY_NARRATION_VOICE_ID: dict[TtsProvider, str] = {
    TtsProvider.KOKORO: "kokoro:af_heart",
    TtsProvider.OPENAI: "openai:alloy",
    TtsProvider.AZURE: "azure:alloy",
    TtsProvider.VERTEX_AI: "vertex_ai:en-US-Studio-O",
}


def _config():
    from app.config import config as app_config

    return app_config


def _ids(provider: TtsProvider, names: str | None) -> list[str]:
    return [
        f"{provider.value}:{name.strip()}"
        for name in (names or "").split(",")
        if name.strip()
    ]


def _usable(
    catalog: VoiceCatalog, provider: TtsProvider, language: str, ids: list[str]
) -> list[str]:
    available = {v.voice_id for v in catalog.for_language(provider, language)}
    return [voice_id for voice_id in dict.fromkeys(ids) if voice_id in available]


def narration_voice_ids(
    catalog: VoiceCatalog, provider: TtsProvider, language: str
) -> list[str]:
    """The narration voice to prefer, configured first, then the legacy one."""
    legacy = LEGACY_NARRATION_VOICE_ID.get(provider)
    candidates = _ids(provider, _config().TTS_NARRATION_VOICE) + (
        [legacy] if legacy else []
    )
    return _usable(catalog, provider, language, candidates)[:1]


def podcast_host_voice_ids(
    catalog: VoiceCatalog, provider: TtsProvider, language: str
) -> list[str]:
    """The configured podcast hosts the model can speak, in slot order."""
    return _usable(
        catalog, provider, language, _ids(provider, _config().TTS_PODCAST_VOICES)
    )


def narration_instructions() -> str | None:
    return (_config().TTS_NARRATION_INSTRUCTIONS or "").strip() or None


def podcast_instructions() -> str | None:
    return (_config().TTS_PODCAST_INSTRUCTIONS or "").strip() or None
