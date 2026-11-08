"""Resolve the configured :class:`TextToSpeech` as a process-wide singleton."""

from __future__ import annotations

from functools import lru_cache

from app.speech.hosted_only import refuse_a_local_speech_service

from .port import TextToSpeech


@lru_cache(maxsize=1)
def get_text_to_speech() -> TextToSpeech:
    """Build the provider selected by ``TTS_SERVICE`` (adapter lazy-imported).

    Every voice is a model the proxy at ``TTS_SERVICE_API_BASE`` forwards
    (``openai/...``, ``vertex_ai/...``), hosted Kokoro included; switching
    provider is a change to ``TTS_SERVICE``. The on-box Kokoro pipeline this
    used to be able to build is gone, so ``local/kokoro`` is refused rather
    than rendering silence.
    """
    from app.config import config as app_config

    service = app_config.TTS_SERVICE
    if not service:
        raise ValueError("TTS_SERVICE is not configured")
    refuse_a_local_speech_service("TTS_SERVICE", service)

    from .adapters.litellm import LiteLlmTextToSpeech

    return LiteLlmTextToSpeech(
        model=service,
        api_base=app_config.TTS_SERVICE_API_BASE,
        api_key=app_config.TTS_SERVICE_API_KEY,
    )
