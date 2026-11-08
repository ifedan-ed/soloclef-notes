"""The TTS providers we carry voices for, and how to name one from config."""

from __future__ import annotations

from enum import StrEnum


class TtsProvider(StrEnum):
    """A speech provider whose voices the catalog enumerates."""

    KOKORO = "kokoro"
    OPENAI = "openai"
    AZURE = "azure"
    VERTEX_AI = "vertex_ai"


# A model whose name says Kokoro speaks Kokoro's voices, whichever proxied
# copy serves it (``openai/openrouter-kokoro-82m-tts``,
# ``openai/local-kokoro-tts``).
# Matching the name is what keeps the roster right, because a Kokoro voice name
# carries the language (``af_*`` American female, ``bm_*`` British male): read
# as an OpenAI model it would be offered coral and ash, which Kokoro rejects.
_KOKORO_MODEL_MARKER = "kokoro"


def provider_from_service(service: str) -> TtsProvider:
    """Map a ``TTS_SERVICE`` string to the roster its voices come from.

    The config value is an OpenAI-style ``provider/model`` string
    (``openai/tts-1``, ``vertex_ai/...``), where the prefix names the API the
    proxy speaks and the rest names the model. Kokoro is the one model family
    whose roster does not follow from the prefix, so it is matched by name.
    """
    normalised = service.strip().lower()
    prefix, _, model = normalised.partition("/")
    if _KOKORO_MODEL_MARKER in (model or prefix):
        return TtsProvider.KOKORO
    return TtsProvider(prefix)
