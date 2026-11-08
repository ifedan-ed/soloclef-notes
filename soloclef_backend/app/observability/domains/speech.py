"""Speech telemetry: spans + durations for audio↔text model calls.

Transcription (STT) during ingestion and synthesis (TTS) during podcast render
both bypass the chat-LLM chokepoint, so they're instrumented here. ``provider``
names the backend that served the call; every one of them is now reached over
the network, so it reads ``litellm``.
"""

from __future__ import annotations

from functools import lru_cache
from typing import Any

from app.observability.core import semconv
from app.observability.signals import metrics as m
from app.observability.signals.tracing import SpanKind, span


def transcription_span(
    *, provider: str, model: str | None = None, extra: dict[str, Any] | None = None
):
    """Span around one speech-to-text call."""
    attrs: dict[str, Any] = {
        semconv.GEN_AI_OPERATION_NAME: "transcription",
        semconv.GEN_AI_PROVIDER_NAME: provider,
    }
    if model:
        attrs[semconv.GEN_AI_REQUEST_MODEL] = model
    if extra:
        attrs.update(extra)
    return span(
        "speech.transcribe",
        kind=SpanKind.CLIENT if SpanKind is not None else None,
        attributes=attrs,
    )


@lru_cache(maxsize=1)
def _transcription_duration():
    return m.get_meter().create_histogram(
        "soloclef.speech.transcription.duration",
        unit="ms",
        description="Duration of Soloclef speech-to-text calls.",
    )


def record_transcription_duration(
    duration_ms: float, *, provider: str, model: str | None = None
) -> None:
    m.record(
        _transcription_duration(),
        duration_ms,
        {
            semconv.GEN_AI_PROVIDER_NAME: provider,
            semconv.GEN_AI_REQUEST_MODEL: model,
        },
    )


def synthesis_span(
    *, provider: str, model: str | None = None, extra: dict[str, Any] | None = None
):
    """Span around one text-to-speech segment synthesis."""
    attrs: dict[str, Any] = {
        semconv.GEN_AI_OPERATION_NAME: "synthesis",
        semconv.GEN_AI_PROVIDER_NAME: provider,
    }
    if model:
        attrs[semconv.GEN_AI_REQUEST_MODEL] = model
    if extra:
        attrs.update(extra)
    return span(
        "speech.synthesize",
        kind=SpanKind.CLIENT if SpanKind is not None else None,
        attributes=attrs,
    )


@lru_cache(maxsize=1)
def _synthesis_duration():
    return m.get_meter().create_histogram(
        "soloclef.speech.synthesis.duration",
        unit="ms",
        description="Duration of Soloclef text-to-speech segment synthesis.",
    )


def record_synthesis_duration(
    duration_ms: float, *, provider: str, model: str | None = None
) -> None:
    m.record(
        _synthesis_duration(),
        duration_ms,
        {
            semconv.GEN_AI_PROVIDER_NAME: provider,
            semconv.GEN_AI_REQUEST_MODEL: model,
        },
    )
