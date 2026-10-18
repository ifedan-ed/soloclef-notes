"""Podcasts run as long as their material supports, voice while drafting, and
are cast from the whole voice roster.

Owner's brief (2026-09-25): as long as possible but never padded; fast to
generate without trading quality; voices chosen per episode, not a fixed pair.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from app.notebooks.studio.briefs import audio_plan
from app.notebooks.studio.options import AudioRequest
from app.podcasts.generation.prompts import draft_segment_prompt, plan_outline_prompt
from app.podcasts.generation.transcript.planning import OutlineSegment
from app.podcasts.rendering import PodcastRenderer
from app.podcasts.rendering.renderer import DEFAULT_MAX_CONCURRENCY
from app.podcasts.schemas import SpeakerRole, SpeakerSpec, TranscriptTurn
from app.podcasts.tts import SynthesizedAudio
from app.podcasts.voices import CatalogVoice, TtsProvider, VoiceCatalog, VoiceGender
from app.podcasts.voices.casting import _Casting, apply_casting, casting_prompt
from app.podcasts.voices.data.openai import OPENAI_VOICES

pytestmark = pytest.mark.unit


# ── length ─────────────────────────────────────────────────────────────────


def test_long_audio_overview_may_run_to_thirty_minutes():
    plan = audio_plan(AudioRequest(kind="audio", format="deep_dive", length="long"))
    assert (plan.min_seconds, plan.max_seconds) == (600, 1800)


def test_outline_is_sized_up_to_the_ceiling_and_forbids_padding(make_spec):
    prompt = plan_outline_prompt(
        spec=make_spec(), target_words=4500, suggested_segments=18, focus=None,
        min_words=1500,
    )
    assert "up to about 4500 words" in prompt
    assert "ideally at least 1500 words" in prompt
    assert "Never pad" in prompt
    assert "plan fewer" in prompt


def test_segment_drafting_prefers_ending_short_to_padding(make_spec):
    prompt = draft_segment_prompt(
        spec=make_spec(),
        segment=OutlineSegment(title="Methods", talking_points=["405 students"], target_words=300),
        position=2,
        total=6,
        recap="The hosts introduced the study.",
    )
    assert "ending the segment short is better than padding it" in prompt


# ── speed ──────────────────────────────────────────────────────────────────


class _CountingTTS:
    def __init__(self) -> None:
        self.calls: list[str] = []
        self.instructions: list[str | None] = []

    @property
    def container(self) -> str:
        return "mp3"

    async def synthesize(self, request):
        self.calls.append(request.text)
        self.instructions.append(request.instructions)
        return SynthesizedAudio(data=b"ID3" + request.text.encode(), container="mp3")


def _openai_catalog() -> VoiceCatalog:
    return VoiceCatalog(list(OPENAI_VOICES))


def _spec(make_spec, *, delivery: str | None = None):
    return make_spec(
        speakers=[
            SpeakerSpec(slot=0, name="Host", role=SpeakerRole.HOST,
                        voice_id="openai:coral", delivery=delivery),
            SpeakerSpec(slot=1, name="Guest", role=SpeakerRole.GUEST,
                        voice_id="openai:onyx"),
        ]
    )


def test_eight_segments_voice_at_once():
    assert DEFAULT_MAX_CONCURRENCY == 8


async def test_segments_voiced_while_drafting_are_not_voiced_again(tmp_path: Path, make_spec):
    tts = _CountingTTS()
    renderer = PodcastRenderer(tts=tts, catalog=_openai_catalog())
    spec = _spec(make_spec)
    turns = [
        TranscriptTurn(speaker=0, text="Welcome to the episode."),
        TranscriptTurn(speaker=1, text="Glad to be here."),
    ]
    await renderer.voice_ahead(renderer.synthesizer(tmp_path), spec=spec, turns=turns)
    assert len(tts.calls) == 2

    # The render keys lines identically, so it finds both in the cache.
    synthesizer = renderer.synthesizer(tmp_path)
    for turn in turns:
        path = await synthesizer.segment(renderer._request_for(spec, turn))
        assert path.exists()
    assert len(tts.calls) == 2


async def test_a_cast_speaker_is_voiced_with_its_own_delivery(tmp_path: Path, make_spec):
    tts = _CountingTTS()
    renderer = PodcastRenderer(tts=tts, catalog=_openai_catalog())
    spec = _spec(make_spec, delivery="Warm and unhurried, curious.")
    await renderer.voice_ahead(
        renderer.synthesizer(tmp_path),
        spec=spec,
        turns=[TranscriptTurn(speaker=0, text="Let us begin.")],
    )
    assert tts.instructions == ["Warm and unhurried, curious."]


# ── casting ────────────────────────────────────────────────────────────────


def _by_name():
    return {v.native_ref: v for v in OPENAI_VOICES}


def test_casting_offers_every_voice_with_its_character(make_spec):
    prompt = casting_prompt(_spec(make_spec), list(OPENAI_VOICES))
    for voice in OPENAI_VOICES:
        assert f"- {voice.native_ref} (" in prompt
    assert "deep, authoritative" in prompt


def test_a_usable_cast_replaces_voices_and_adds_delivery(make_spec):
    spec = _spec(make_spec)
    cast = apply_casting(
        spec,
        _Casting.model_validate({"speakers": [
            {"slot": 0, "voice": "Marin", "delivery": "Bright and curious."},
            {"slot": 1, "voice": "cedar", "delivery": "Calm, expert, measured."},
        ]}),
        _by_name(),
    )
    assert [s.voice_id for s in cast.speakers] == ["openai:marin", "openai:cedar"]
    assert cast.speakers[1].delivery == "Calm, expert, measured."
    assert [s.name for s in cast.speakers] == ["Host", "Guest"]


@pytest.mark.parametrize(
    "speakers",
    [
        [{"slot": 0, "voice": "coral"}, {"slot": 1, "voice": "coral"}],  # same voice
        [{"slot": 0, "voice": "coral"}, {"slot": 1, "voice": "morgan"}],  # unknown
        [{"slot": 0, "voice": "coral"}],  # a speaker left uncast
    ],
)
def test_an_unusable_cast_keeps_the_briefs_voices(make_spec, speakers):
    spec = _spec(make_spec)
    kept = apply_casting(spec, _Casting.model_validate({"speakers": speakers}), _by_name())
    assert kept is spec


def test_a_delivery_note_is_capped(make_spec):
    spec = _spec(make_spec)
    cast = apply_casting(
        spec,
        _Casting.model_validate({"speakers": [
            {"slot": 0, "voice": "sage", "delivery": "slow " * 200},
            {"slot": 1, "voice": "ash", "delivery": ""},
        ]}),
        _by_name(),
    )
    assert len(cast.speakers[0].delivery) <= 300
    assert cast.speakers[1].delivery is None


def test_a_non_openai_catalog_is_named_by_display_name():
    voice = CatalogVoice(
        voice_id="vertex_ai:en-US-Studio-O", provider=TtsProvider.VERTEX_AI,
        language="en-US", display_name="Studio O", gender=VoiceGender.FEMALE,
        native_ref={"name": "en-US-Studio-O"},
    )
    from app.podcasts.voices.casting import _name

    assert _name(voice) == "studio o"
