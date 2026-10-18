"""The token ceiling used to truncate a document before it is embedded."""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.utils import document_converters

pytestmark = pytest.mark.unit


def _with_model(monkeypatch, model) -> None:
    monkeypatch.setattr(
        document_converters,
        "config",
        SimpleNamespace(embedding_model_instance=model),
    )


def test_a_model_that_states_its_limit_is_believed(monkeypatch):
    _with_model(monkeypatch, SimpleNamespace(max_seq_length=512))

    assert document_converters._get_embedding_max_tokens() == 512


def test_the_fallback_leaves_headroom_below_8192(monkeypatch):
    _with_model(monkeypatch, SimpleNamespace())
    monkeypatch.delenv("EMBEDDING_MAX_TOKENS", raising=False)

    assert document_converters._get_embedding_max_tokens() == 6000


def test_the_fallback_is_overridable(monkeypatch):
    _with_model(monkeypatch, SimpleNamespace())
    monkeypatch.setenv("EMBEDDING_MAX_TOKENS", "7000")

    assert document_converters._get_embedding_max_tokens() == 7000
