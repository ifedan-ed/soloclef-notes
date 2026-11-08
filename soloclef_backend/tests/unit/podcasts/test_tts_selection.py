"""Which adapter and which voice roster ``TTS_SERVICE`` selects.

Everything hosted is reached through the proxy, so the service string carries
two decisions: the adapter that makes the call, and the roster the voices come
from. The second is the one that used to be wrong -- a Kokoro model served
over the OpenAI speech API was read as an OpenAI model and offered coral and
ash, which Kokoro rejects.
"""

from __future__ import annotations

import pytest

from app.podcasts.tts.adapters.litellm import LiteLlmTextToSpeech
from app.podcasts.tts.factory import get_text_to_speech
from app.podcasts.voices import TtsProvider, get_voice_catalog, provider_from_service
from app.speech.hosted_only import LocalSpeechServiceError

pytestmark = pytest.mark.unit

PROXIED_KOKORO = "openai/openrouter-kokoro-82m-tts"


@pytest.fixture(autouse=True)
def _uncached():
    get_text_to_speech.cache_clear()
    yield
    get_text_to_speech.cache_clear()


@pytest.fixture
def service(monkeypatch):
    def set_(value: str) -> None:
        from app.config import config as app_config

        monkeypatch.setattr(app_config, "TTS_SERVICE", value)
        monkeypatch.setattr(app_config, "TTS_SERVICE_API_BASE", "http://proxy:4000/v1")
        monkeypatch.setattr(app_config, "TTS_SERVICE_API_KEY", "sk-proxy-test")

    return set_


def test_a_proxied_model_is_synthesised_through_the_proxy_adapter(service):
    service(PROXIED_KOKORO)
    tts = get_text_to_speech()

    assert isinstance(tts, LiteLlmTextToSpeech)
    assert tts.container == "mp3"


def test_a_proxied_kokoro_model_offers_kokoro_voices_not_openais(service):
    service(PROXIED_KOKORO)
    provider = provider_from_service(PROXIED_KOKORO)
    names = {voice.native_ref for voice in get_voice_catalog().for_provider(provider)}

    assert provider == TtsProvider.KOKORO
    assert {"af_heart", "am_adam"} <= names
    assert not {"coral", "ash"} & names


def test_an_openai_model_still_offers_openai_voices():
    provider = provider_from_service("openai/openai-gpt-4o-mini-tts")

    assert provider == TtsProvider.OPENAI


def test_a_local_service_is_refused_naming_the_setting(service):
    """On-box Kokoro is gone: ``local/kokoro`` stops rather than going quiet."""
    service("local/kokoro")

    with pytest.raises(LocalSpeechServiceError) as refused:
        get_text_to_speech()

    assert "TTS_SERVICE" in str(refused.value)
