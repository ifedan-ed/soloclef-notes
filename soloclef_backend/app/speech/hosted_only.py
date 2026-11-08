"""Speech models are reached over the network, never loaded into this process.

``TTS_SERVICE`` and ``STT_SERVICE`` could once name a model that ran on this
box: ``local/kokoro`` synthesised with the Kokoro package, ``local/<size>``
transcribed with Faster-Whisper, and an unset ``STT_SERVICE`` meant
``local/base``. Both packages are gone, and torch with them, so a deployment
that still names one is refused here rather than falling silent or reaching for
a model nobody chose -- the same rule, and the same register, as a bare
embedding model name (``app/embedding_roles/settings.py``).
"""

from __future__ import annotations

LOCAL_PREFIX = "local/"

# What to name instead, per setting: the hosted models this deployment runs on.
_SERVED_EXAMPLE = {
    "TTS_SERVICE": "openai/openrouter-kokoro-82m-tts",
    "STT_SERVICE": "openai/groq-whisper-large-v3",
}


class LocalSpeechServiceError(ValueError):
    """A service name that would run a speech model inside this process."""


def refuse_a_local_speech_service(setting: str, service: str | None) -> None:
    """Stop when ``setting`` names a speech model on this box.

    An unset value is not this check's business: the setting is required where
    the service is used, and the refusal there names it.
    """
    service = (service or "").strip()
    if not service.startswith(LOCAL_PREFIX):
        return
    example = _SERVED_EXAMPLE.get(setting, "openai/<the proxy's model name>")
    raise LocalSpeechServiceError(
        f"{setting}={service!r} names a speech model on this box. This server "
        "has no local inference: every speech model is reached through a "
        f"provider, and ours go through the proxy at {setting}_API_BASE. Set it "
        f"to a served model, for example {setting}={example}."
    )
