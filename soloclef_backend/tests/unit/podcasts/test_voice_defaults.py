"""The configured voices, and what happens when they are not the provider's.

A voice name only means something for one provider, so there is no voice in
code to default to: unset leaves the choice to the catalog, and a name the
active provider does not have is raised rather than quietly replaced.

The proxy is an external boundary: its /model/info reply is faked.
"""

from __future__ import annotations

import hashlib
import json
import shutil

import httpx
import pytest

from app.config import config
from app.podcasts.generation.brief.nodes import propose_spec
from app.podcasts.rendering.cache import SegmentCache
from app.podcasts.tts import SynthesisRequest
from app.podcasts.tts.adapters import litellm as litellm_adapter
from app.podcasts.voices import TtsProvider, get_voice_catalog, openai_roster
from app.podcasts.voices.defaults import (
    narration_instructions,
    narration_voice_ids,
    podcast_host_voice_ids,
)

from .hosted_speech_stub import speech_mp3

pytestmark = pytest.mark.unit

MINI_VOICES = [
    "alloy", "ash", "ballad", "coral", "echo", "fable", "nova", "onyx", "sage",
    "shimmer", "verse", "marin", "cedar",
]  # fmt: skip


@pytest.fixture
def tts(monkeypatch):
    """Point TTS at a model, with LiteLLM answering /model/info (or not)."""

    def set_(
        service: str,
        *,
        model_info: dict | None = None,
        voices: tuple[str, ...] = ("coral", "ash"),
    ) -> None:
        monkeypatch.setattr(config, "TTS_SERVICE", service)
        monkeypatch.setattr(config, "TTS_SERVICE_API_BASE", "http://litellm:4000/v1")
        monkeypatch.setattr(config, "TTS_SERVICE_API_KEY", "sk-test")
        monkeypatch.setattr(config, "TTS_NARRATION_VOICE", voices and voices[0])
        monkeypatch.setattr(config, "TTS_PODCAST_VOICES", voices and ",".join(voices))

        def fake_get(url, headers, timeout):
            assert url == "http://litellm:4000/v1/model/info"
            assert headers == {"Authorization": "Bearer sk-test"}
            if model_info is None:
                raise httpx.ConnectError("litellm down")
            return httpx.Response(
                200, json=model_info, request=httpx.Request("GET", url)
            )

        monkeypatch.setattr(openai_roster.httpx, "get", fake_get)
        openai_roster.openai_voice_names.cache_clear()
        get_voice_catalog.cache_clear()

    yield set_
    openai_roster.openai_voice_names.cache_clear()
    get_voice_catalog.cache_clear()


def _openai_names() -> list[str]:
    return [v.native_ref for v in get_voice_catalog().for_provider(TtsProvider.OPENAI)]


def test_the_catalog_lists_the_voices_litellm_reports_for_the_model(tts):
    tts(
        "openai/openai-gpt-4o-mini-tts",
        model_info={
            "data": [
                {"model_name": "openai-tts-1-hd", "model_info": {"voices": ["alloy"]}},
                {
                    "model_name": "openai-gpt-4o-mini-tts",
                    "model_info": {"voices": MINI_VOICES},
                },
            ]
        },
    )

    assert sorted(_openai_names()) == sorted(MINI_VOICES)


@pytest.mark.parametrize(
    ("service", "has", "lacks"),
    [
        ("openai/openai-gpt-4o-mini-tts", {"coral", "ash", "marin", "cedar"}, set()),
        ("openai/openai-tts-1-hd", {"coral", "ash", "alloy"}, {"ballad", "marin"}),
    ],
)
def test_without_litellm_the_static_list_follows_the_model_name(
    tts, service, has, lacks
):
    tts(service, model_info=None)

    names = set(_openai_names())
    assert has <= names and not (lacks & names)


def test_narration_uses_the_configured_voice(tts):
    tts("openai/openai-gpt-4o-mini-tts", model_info=None)
    catalog = get_voice_catalog()

    assert narration_voice_ids(catalog, TtsProvider.OPENAI, "en") == ["openai:coral"]


def test_a_voice_the_provider_does_not_have_names_the_setting(tts, monkeypatch):
    """The fallback this closes once narrated in a voice nobody chose."""
    tts("openai/openai-gpt-4o-mini-tts", model_info=None)
    catalog = get_voice_catalog()
    monkeypatch.setattr(config, "TTS_NARRATION_VOICE", "af_heart")

    with pytest.raises(ValueError, match="TTS_NARRATION_VOICE"):
        narration_voice_ids(catalog, TtsProvider.OPENAI, "en")


def test_a_host_the_provider_does_not_have_names_the_setting(tts, monkeypatch):
    tts("openai/openai-gpt-4o-mini-tts", model_info=None)
    catalog = get_voice_catalog()
    monkeypatch.setattr(config, "TTS_PODCAST_VOICES", "coral,af_heart")

    with pytest.raises(ValueError, match="TTS_PODCAST_VOICES"):
        podcast_host_voice_ids(catalog, TtsProvider.OPENAI, "en")


def test_unset_leaves_the_choice_to_the_catalog(tts):
    """Choosing nothing is not the same as choosing a voice that cannot work."""
    tts("openai/openai-gpt-4o-mini-tts", model_info=None, voices=())
    catalog = get_voice_catalog()

    assert narration_voice_ids(catalog, TtsProvider.OPENAI, "en") == ["openai:alloy"]
    assert podcast_host_voice_ids(catalog, TtsProvider.OPENAI, "en") == []


def test_kokoro_keeps_its_own_narration_voice(tts):
    tts("openai/openrouter-kokoro-82m-tts", voices=())
    catalog = get_voice_catalog()
    assert narration_voice_ids(catalog, TtsProvider.KOKORO, "en") == ["kokoro:af_heart"]


def test_a_new_podcast_is_hosted_by_coral_and_ash(tts):
    tts("openai/openai-gpt-4o-mini-tts", model_info=None)
    catalog = get_voice_catalog()
    assert podcast_host_voice_ids(catalog, TtsProvider.OPENAI, "en") == [
        "openai:coral",
        "openai:ash",
    ]

    spec = propose_spec(
        None,
        {"configurable": {"speaker_count": 2, "min_seconds": 60, "max_seconds": 120}},
    )["spec"]

    assert [s.voice_id for s in spec.speakers] == ["openai:coral", "openai:ash"]


def test_voices_chosen_before_are_kept_and_the_rest_filled_by_default(tts):
    tts("openai/openai-gpt-4o-mini-tts", model_info=None)

    spec = propose_spec(
        None,
        {
            "configurable": {
                "speaker_count": 2,
                "min_seconds": 60,
                "max_seconds": 120,
                "last_used_language": "en",
                "last_used_voices": ["openai:sage"],
            }
        },
    )["spec"]

    assert [s.voice_id for s in spec.speakers] == ["openai:sage", "openai:coral"]


@pytest.mark.skipif(shutil.which("ffmpeg") is None, reason="needs ffmpeg")
@pytest.mark.parametrize(
    ("model", "sent"),
    [("openai/openai-gpt-4o-mini-tts", True), ("openai/openai-tts-1-hd", False)],
)
async def test_instructions_go_only_to_a_model_that_takes_them(
    monkeypatch, model, sent
):
    seen = {}

    async def aspeech(**kwargs):
        seen.update(kwargs)
        return type("R", (), {"content": speech_mp3()})()

    monkeypatch.setattr("litellm.aspeech", aspeech)
    tts = litellm_adapter.LiteLlmTextToSpeech(model=model)

    await tts.synthesize(
        SynthesisRequest(
            text="Hello", voice="coral", language="en", instructions="Warm lecturer."
        )
    )

    assert ("instructions" in seen) is sent
    assert seen["voice"] == "coral"


def test_the_narration_tone_defaults_to_a_warm_medical_lecturer():
    assert "warm, clear" in narration_instructions()
    assert "medical lecturer" in narration_instructions()


def test_segments_cached_before_instructions_keep_their_keys(tmp_path):
    cache = SegmentCache(tmp_path)
    request = SynthesisRequest(text="Right.", voice="alloy", language="en")
    old_key = hashlib.sha256(
        json.dumps(
            {"voice": "alloy", "language": "en", "speed": 1.0, "text": "Right."},
            sort_keys=True,
            ensure_ascii=True,
        ).encode()
    ).hexdigest()

    assert cache.key(request) == old_key
    styled = SynthesisRequest(
        text="Right.", voice="alloy", language="en", instructions="Warm."
    )
    assert cache.key(styled) != old_key


GROK_NAMES = ["eve", "ara", "rex", "sal", "leo"]


def test_grok_lists_its_own_voices_from_litellm(tts):
    tts(
        "openai/openrouter-grok-voice-tts-1.0",
        model_info={
            "data": [
                {
                    "model_name": "openrouter-grok-voice-tts-1.0",
                    "model_info": {"voices": GROK_NAMES},
                }
            ]
        },
    )

    assert sorted(_openai_names()) == sorted(GROK_NAMES)


def test_grok_without_litellm_still_knows_its_voices(tts):
    tts("openai/openrouter-grok-voice-tts-1.0", model_info=None)

    assert sorted(_openai_names()) == sorted(GROK_NAMES)


def test_grok_narrates_with_ara_and_hosts_with_ara_and_rex(tts, monkeypatch):
    tts("openai/openrouter-grok-voice-tts-1.0", model_info=None)
    monkeypatch.setattr(config, "TTS_NARRATION_VOICE", "ara")
    monkeypatch.setattr(config, "TTS_PODCAST_VOICES", "ara,rex")
    catalog = get_voice_catalog()

    assert narration_voice_ids(catalog, TtsProvider.OPENAI, "en") == ["openai:ara"]
    assert podcast_host_voice_ids(catalog, TtsProvider.OPENAI, "en") == [
        "openai:ara",
        "openai:rex",
    ]
