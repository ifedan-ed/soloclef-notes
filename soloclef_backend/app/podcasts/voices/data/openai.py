"""Voices of the OpenAI-compatible TTS models: each speaks any language.

The roster is gpt-4o-mini-tts's thirteen voices (tts-1 and tts-1-hd speak nine
of them) plus the five of xAI's Grok Voice TTS, which LiteLLM serves on the
same OpenAI speech route. The catalog keeps only those the configured model
has (see ``openai_roster``). Grok's voices share the ``openai:`` id prefix
because they go through the same provider path.

OpenAI voices follow the language of the input text rather than being tied to a
locale, so they are tagged :data:`ANY_LANGUAGE` and match every brief. The
``native_ref`` is the plain voice name the API expects.
"""

from __future__ import annotations

from ..provider import TtsProvider
from ..voice import ANY_LANGUAGE, CatalogVoice, VoiceGender


def _voice(name: str, display: str, gender: VoiceGender) -> CatalogVoice:
    return CatalogVoice(
        voice_id=f"openai:{name}",
        provider=TtsProvider.OPENAI,
        language=ANY_LANGUAGE,
        display_name=display,
        gender=gender,
        native_ref=name,
    )


OPENAI_VOICES: tuple[CatalogVoice, ...] = (
    _voice("alloy", "Alloy", VoiceGender.NEUTRAL),
    _voice("ash", "Ash", VoiceGender.MALE),
    _voice("ballad", "Ballad", VoiceGender.MALE),
    _voice("coral", "Coral", VoiceGender.FEMALE),
    _voice("echo", "Echo", VoiceGender.MALE),
    _voice("fable", "Fable", VoiceGender.NEUTRAL),
    _voice("nova", "Nova", VoiceGender.FEMALE),
    _voice("onyx", "Onyx", VoiceGender.MALE),
    _voice("sage", "Sage", VoiceGender.FEMALE),
    _voice("shimmer", "Shimmer", VoiceGender.FEMALE),
    _voice("verse", "Verse", VoiceGender.MALE),
    _voice("marin", "Marin", VoiceGender.FEMALE),
    _voice("cedar", "Cedar", VoiceGender.MALE),
)

# xAI Grok Voice TTS (chosen 2026-09-26 after the medical-script accuracy
# test: no clinically significant misreadings for ara, leo and eve).
GROK_VOICES: tuple[CatalogVoice, ...] = (
    _voice("ara", "Ara", VoiceGender.FEMALE),
    _voice("eve", "Eve", VoiceGender.FEMALE),
    _voice("leo", "Leo", VoiceGender.MALE),
    _voice("rex", "Rex", VoiceGender.MALE),
    _voice("sal", "Sal", VoiceGender.NEUTRAL),
)

# Every voice an OpenAI-compatible model may list.
OPENAI_COMPATIBLE_VOICES: tuple[CatalogVoice, ...] = (*OPENAI_VOICES, *GROK_VOICES)

# The voices tts-1 and tts-1-hd accept, when LiteLLM cannot be asked.
TTS1_VOICE_NAMES: frozenset[str] = frozenset(
    {"alloy", "ash", "coral", "echo", "fable", "nova", "onyx", "sage", "shimmer"}
)
