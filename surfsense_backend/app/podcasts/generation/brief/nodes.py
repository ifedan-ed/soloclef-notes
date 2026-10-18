"""Brief-planning node: propose a full spec from deterministic defaults.

``propose_spec`` is pure resolution — it never spends tokens. It reuses the
user's last-used language/voices when available and otherwise falls back to
English, so the brief gate opens pre-filled and the common case needs no edits.
"""

from __future__ import annotations

from typing import Any

from langchain_core.runnables import RunnableConfig

from app.config import config as app_config
from app.podcasts.resolution import (
    DEFAULT_LANGUAGE,
    LanguageContext,
    resolve_language,
    resolve_voices,
)
from app.podcasts.schemas import (
    DurationTarget,
    PodcastSpec,
    PodcastStyle,
    SpeakerRole,
    SpeakerSpec,
    normalize_language_tag,
)
from app.podcasts.voices import (
    TtsProvider,
    VoiceCatalog,
    get_voice_catalog,
    provider_from_service,
)
from app.podcasts.voices.defaults import podcast_host_voice_ids

from .config import BriefConfig
from .state import BriefState

# Default role per speaker slot; extra speakers beyond the list fall back to guest.
_ROLE_BY_SLOT = (
    SpeakerRole.HOST,
    SpeakerRole.GUEST,
    SpeakerRole.EXPERT,
    SpeakerRole.COHOST,
    SpeakerRole.NARRATOR,
)


def propose_spec(state: BriefState, config: RunnableConfig) -> dict[str, Any]:
    """Build a complete :class:`PodcastSpec` from the resolved defaults."""
    brief = BriefConfig.from_runnable_config(config)
    provider = _active_provider()
    catalog = get_voice_catalog()

    language = _supported_language(
        last_used=brief.last_used_language,
        provider=provider,
        catalog=catalog,
    )
    voices = resolve_voices(
        catalog=catalog,
        provider=provider,
        language=language,
        speaker_count=brief.speaker_count,
        preferred=_preferred_voices(
            brief.last_used_voices,
            podcast_host_voice_ids(catalog, provider, language),
        ),
    )

    speakers = [
        SpeakerSpec(
            slot=slot,
            name=_default_name(slot),
            role=_role_for(slot),
            voice_id=voice.voice_id,
        )
        for slot, voice in enumerate(voices)
    ]
    spec = PodcastSpec(
        language=language,
        style=PodcastStyle.CONVERSATIONAL,
        speakers=speakers,
        duration=DurationTarget(
            min_seconds=brief.min_seconds, max_seconds=brief.max_seconds
        ),
        focus=brief.focus,
    )
    return {"spec": spec}


def _preferred_voices(last_used: list[str] | None, defaults: list[str]) -> list[str]:
    """The last-used voice per slot, else the configured host for that slot."""
    last_used = list(last_used or [])
    fill = [voice for voice in defaults if voice not in last_used]
    return last_used + fill[: max(0, len(defaults) - len(last_used))]


def _active_provider() -> TtsProvider:
    service = app_config.TTS_SERVICE
    if not service:
        raise ValueError("TTS_SERVICE is not configured")
    return provider_from_service(service)


def _supported_language(
    *,
    last_used: str | None,
    provider: TtsProvider,
    catalog: VoiceCatalog,
) -> str:
    raw = resolve_language(LanguageContext(last_used=last_used))
    try:
        language = normalize_language_tag(raw)
    except ValueError:
        language = DEFAULT_LANGUAGE
    if not catalog.supports_language(provider, language):
        return DEFAULT_LANGUAGE
    return language


def _role_for(slot: int) -> SpeakerRole:
    return _ROLE_BY_SLOT[slot] if slot < len(_ROLE_BY_SLOT) else SpeakerRole.GUEST


def _default_name(slot: int) -> str:
    role = _role_for(slot)
    label = role.value.replace("cohost", "co-host").title()
    return label if slot < len(_ROLE_BY_SLOT) else f"{label} {slot}"
