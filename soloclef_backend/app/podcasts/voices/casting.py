"""Cast a Studio podcast's voices from the whole roster, not a fixed pair.

Studio has no voice picker, so every audio overview used to be voiced by the
same two configured hosts. Before drafting, one short call to the quick-tasks
model reads the episode (title, format, speaker roles, the opening of its
sources) and picks a distinct voice per speaker from every voice the active TTS
model speaks, with a one-line delivery note per speaker for models that take a
speaking style. Anything unusable in its reply keeps the voices already in the
brief, so casting can improve an episode but never stop one.
"""

from __future__ import annotations

import logging

from langchain_core.messages import HumanMessage, SystemMessage
from pydantic import BaseModel, Field

from app.podcasts.schemas import PodcastSpec
from app.utils.structured_output import invoke_json

from .catalog import VoiceCatalog
from .provider import TtsProvider
from .voice import CatalogVoice

logger = logging.getLogger(__name__)

_SOURCE_EXCERPT_CHARS = 3_000
_MAX_DELIVERY_CHARS = 300

# How each gpt-4o-mini-tts and Grok voice tends to sound, so the choice is not made on
# a name alone. Voices without an entry are offered by name and gender.
VOICE_CHARACTER: dict[str, str] = {
    "alloy": "balanced, neutral, even",
    "ash": "clear, assured, conversational",
    "ballad": "soft, warm, expressive",
    "coral": "warm, friendly, bright",
    "echo": "resonant, calm, measured",
    "fable": "animated, storytelling",
    "nova": "bright, energetic, youthful",
    "onyx": "deep, authoritative",
    "sage": "calm, thoughtful, gentle",
    "shimmer": "light, clear, upbeat",
    "verse": "expressive, dynamic",
    "marin": "natural, polished, professional",
    "cedar": "natural, grounded, steady",
    # Grok Voice TTS
    "ara": "warm, friendly, clear",
    "eve": "energetic, upbeat, bright",
    "leo": "authoritative, strong, measured",
    "rex": "confident, clear, professional",
    "sal": "smooth, balanced, neutral",
}


class _CastSpeaker(BaseModel):
    slot: int
    voice: str = Field(..., min_length=1)
    delivery: str = Field(default="", max_length=2_000)


class _Casting(BaseModel):
    speakers: list[_CastSpeaker] = Field(..., min_length=1)


def _name(voice: CatalogVoice) -> str:
    """What the model calls a voice: its provider name, else its display name."""
    ref = voice.native_ref
    return (ref if isinstance(ref, str) else voice.display_name).strip().lower()


def _roster_line(voice: CatalogVoice) -> str:
    character = VOICE_CHARACTER.get(_name(voice))
    described = f", {character}" if character else ""
    return f"- {_name(voice)} ({voice.gender.value}{described})"


def casting_prompt(spec: PodcastSpec, voices: list[CatalogVoice]) -> str:
    speakers = "\n".join(
        f"- slot {s.slot}: {s.name}, role {s.role.value}" for s in spec.speakers
    )
    roster = "\n".join(_roster_line(v) for v in voices)
    return f"""\
You are casting the voices for a podcast episode before it is recorded.

Format: {spec.style.value}. Language: {spec.language}.
Speakers:
{speakers}

Available voices (name, gender, character):
{roster}

Choose one voice per speaker. Give different speakers clearly different voices,
contrasting in gender or character, so a listener can always tell who is
talking. Match each voice to the speaker's role and to the subject and tone of
the material. For each speaker also write a one-sentence delivery note in
English for the voice actor: pace, warmth, energy and attitude (for example
"Warm and unhurried, curious, with a smile in the voice"). Do not ask for
accents or impersonations.

Respond with strict JSON and nothing else:
{{"speakers": [{{"slot": 0, "voice": "<name>", "delivery": "..."}}]}}
"""


async def cast_voices(
    llm,
    spec: PodcastSpec,
    *,
    title: str,
    source_content: str,
    catalog: VoiceCatalog,
    provider: TtsProvider,
) -> PodcastSpec:
    """``spec`` with a cast chosen for it, or unchanged if casting fails."""
    voices = catalog.for_language(provider, spec.language)
    if llm is None or len(voices) < 2:
        return spec
    by_name = {_name(v): v for v in voices}
    excerpt = " ".join((source_content or "").split())[:_SOURCE_EXCERPT_CHARS]
    messages = [
        SystemMessage(content=casting_prompt(spec, voices)),
        HumanMessage(content=f"Episode title: {title}\n\nSource opening:\n{excerpt}"),
    ]
    try:
        casting = await invoke_json(llm, messages, _Casting)
    except Exception:
        logger.warning(
            "Voice casting failed; keeping the brief's voices", exc_info=True
        )
        return spec
    return apply_casting(spec, casting, by_name)


def apply_casting(
    spec: PodcastSpec, casting: _Casting, by_name: dict[str, CatalogVoice]
) -> PodcastSpec:
    """Apply a reply only if it voices every speaker with distinct known voices."""
    chosen = {c.slot: c for c in casting.speakers}
    slots = [s.slot for s in spec.speakers]
    names = [chosen[s].voice.strip().lower() if s in chosen else "" for s in slots]
    if any(n not in by_name for n in names) or len(set(names)) != len(names):
        logger.info(
            "Voice casting reply unusable (%s); keeping the brief's voices", names
        )
        return spec
    speakers = [
        speaker.model_copy(
            update={
                "voice_id": by_name[name].voice_id,
                "delivery": " ".join(chosen[speaker.slot].delivery.split())[
                    :_MAX_DELIVERY_CHARS
                ]
                or None,
            }
        )
        for speaker, name in zip(spec.speakers, names, strict=True)
    ]
    return spec.model_copy(update={"speakers": speakers})
