"""Fixtures for the video presentation agent's unit tests.

``Config`` reads the environment once at import, so a test that sets
``os.environ`` changes nothing. Every knob here is therefore set on the config
singleton itself, which is also how the rest of the suite does it.
"""

from __future__ import annotations

import pytest

from app.config import config


@pytest.fixture
def tts_service(monkeypatch):
    """Point the agent at a TTS provider for the duration of one test."""

    def _set(
        service: str | None = "openai/openrouter-kokoro-82m-tts",
        narration_voice: str | None = None,
    ) -> None:
        monkeypatch.setattr(config, "TTS_SERVICE", service)
        # There is no voice in code to default to, so a test that expects a
        # configured one says which (app/podcasts/voices/defaults.py).
        monkeypatch.setattr(config, "TTS_NARRATION_VOICE", narration_voice)

    return _set


@pytest.fixture
def default_language(monkeypatch):
    """Set the operator's fallback narration language."""

    def _set(language: str) -> None:
        monkeypatch.setattr(config, "VIDEO_PRESENTATION_DEFAULT_LANGUAGE", language)

    return _set
