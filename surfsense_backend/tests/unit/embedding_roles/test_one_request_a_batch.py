"""A batch to a hosted model is one call, sent once: a host that hangs costs
one timeout, then the batch falls back and the breaker keeps the next ones
away.

Chonkie's own ``embed_batch`` tries the texts of a failed batch again one by
one, and the OpenAI client under litellm tries each request three times;
these pin that the document path does neither."""

from __future__ import annotations

from types import SimpleNamespace

import litellm
import numpy as np
import pytest
from chonkie.embeddings.catsu import CatsuEmbeddings
from chonkie.embeddings.litellm import LiteLLMEmbeddings
from chonkie.embeddings.openai import OpenAIEmbeddings

from app.config import config
from app.embedding_roles.clients import (
    SingleAttemptOpenAI,
    document_host_kwargs,
    embed_batch_once,
    probe_once,
)
from app.utils.document_converters import embed_texts

from .conftest import DIMENSION

pytestmark = pytest.mark.unit


def _litellm_client(timeout: float = 20.0) -> LiteLLMEmbeddings:
    """A LiteLLMEmbeddings as config builds it, without its construction-time
    probe and tokenizer download."""
    client = LiteLLMEmbeddings.__new__(LiteLLMEmbeddings)
    client.model = "openai/document-host"
    client.api_key = None
    client.api_base = "http://litellm:4000/v1"
    client.timeout = timeout
    client.max_retries = 3
    client._batch_size = 128
    client._extra_kwargs = {}
    client._dimension = DIMENSION
    return client


class _HangingHost:
    """``litellm.embedding`` against a host that never answers."""

    def __init__(self) -> None:
        self.calls: list[dict] = []

    def __call__(self, **kwargs):
        self.calls.append(kwargs)
        raise litellm.exceptions.Timeout(
            message="Request timed out", model=kwargs["model"], llm_provider="openai"
        )


def test_a_hanging_host_costs_one_request_not_one_per_text(monkeypatch):
    hang = _HangingHost()
    monkeypatch.setattr(litellm, "embedding", hang)
    client = _litellm_client()
    texts = [f"text {i}" for i in range(8)]

    with pytest.raises(litellm.exceptions.Timeout):
        embed_batch_once(client, texts)
    assert len(hang.calls) == 1
    assert hang.calls[0]["input"] == texts
    assert hang.calls[0]["timeout"] == 20.0
    assert hang.calls[0]["api_base"] == "http://litellm:4000/v1"

    # What chonkie's own batch call does: the batch, then its first text alone.
    hang.calls.clear()
    with pytest.raises(RuntimeError):
        client.embed_batch(texts)
    assert len(hang.calls) == 2


def test_a_hanging_document_host_is_tried_once_then_skipped(roles, monkeypatch):
    hang = _HangingHost()
    monkeypatch.setattr(litellm, "embedding", hang)
    monkeypatch.setattr(config, "document_embedding_model_instance", _litellm_client())

    embed_texts([f"chunk {i}" for i in range(8)])
    embed_texts([f"next document {i}" for i in range(8)])

    assert len(hang.calls) == 1, "one timeout, then the breaker"
    assert len(roles.query.texts) == 16, "both documents indexed by the fallback"


def test_an_answer_comes_back_in_the_texts_order(monkeypatch):
    def answers(**kwargs):
        rows = [
            {"index": i, "embedding": [float(i)] * DIMENSION}
            for i in range(len(kwargs["input"]))
        ]
        return SimpleNamespace(data=list(reversed(rows)))

    monkeypatch.setattr(litellm, "embedding", answers)

    vectors = embed_batch_once(_litellm_client(), ["a", "b", "c"])

    assert [float(v[0]) for v in vectors] == [0.0, 1.0, 2.0]
    assert all(v.dtype == np.float32 for v in vectors)


class _CatsuHost:
    def __init__(self, *, fail: bool) -> None:
        self.fail = fail
        self.calls: list[list[str]] = []

    def embed(self, *, model, input, provider):
        self.calls.append(list(input))
        if self.fail:
            raise TimeoutError("timed out")
        return SimpleNamespace(
            to_numpy=lambda: np.ones((len(input), DIMENSION), dtype=np.float32)
        )


def _catsu_client(host: _CatsuHost) -> CatsuEmbeddings:
    client = CatsuEmbeddings.__new__(CatsuEmbeddings)
    client.model = "text-embedding-3-small"
    client.provider = "openai"
    client.client = host
    client._batch_size = 128
    return client


@pytest.mark.parametrize("wrapped", [False, True])
def test_the_catsu_clients_send_one_request_too(wrapped):
    host = _CatsuHost(fail=True)
    client = _catsu_client(host)
    if wrapped:  # openai://, cohere:// and the rest wrap a CatsuEmbeddings
        wrapper = OpenAIEmbeddings.__new__(OpenAIEmbeddings)
        wrapper._catsu = client
        client = wrapper

    with pytest.raises(TimeoutError):
        embed_batch_once(client, [f"text {i}" for i in range(8)])
    assert len(host.calls) == 1

    host.fail = False
    assert len(embed_batch_once(client, ["a", "b"])) == 2


def test_any_other_client_is_asked_through_its_own_batch_call():
    class Other:
        def embed_batch(self, texts):
            return [np.zeros(DIMENSION) for _ in texts]

    assert len(embed_batch_once(Other(), ["a", "b"])) == 2


def test_a_document_host_with_a_fallback_gets_a_client_that_sends_once(monkeypatch):
    monkeypatch.setenv("OPENAI_API_KEY", "sk-test")
    model = "litellm://openai/openrouter-bge-m3"

    extra = document_host_kwargs(
        model, {"api_base": "http://litellm:4000/v1"}, timeout=20.0, send_once=True
    )

    assert extra["timeout"] == 20.0
    single = extra["client"]
    assert isinstance(single, SingleAttemptOpenAI)
    assert str(single.base_url).rstrip("/") == "http://litellm:4000/v1"
    single.max_retries = 2  # what litellm does to any client it is handed
    assert single.max_retries == 0


@pytest.mark.parametrize(
    ("model", "send_once", "expected"),
    [
        # No fallback: the client keeps its own retries.
        ("litellm://openai/openrouter-bge-m3", False, {"timeout": 20.0}),
        # Another litellm provider: no OpenAI client to hand it.
        ("litellm://ollama/bge-m3", True, {"timeout": 20.0}),
        # The Catsu clients are asked for no retries.
        ("openai://text-embedding-3-small", True, {"timeout": 20.0, "max_retries": 0}),
        ("jina://jina-embeddings-v3", True, {"max_retries": 0}),  # takes no timeout
        ("sentence-transformers/all-MiniLM-L6-v2", True, {}),
        ("nosuchprovider://x", True, {}),
    ],
)
def test_what_each_kind_of_document_client_is_given(
    monkeypatch, model, send_once, expected
):
    monkeypatch.setenv("OPENAI_API_KEY", "sk-test")

    extra = document_host_kwargs(model, {}, timeout=20.0, send_once=send_once)

    assert extra == expected


def test_without_a_key_litellm_builds_its_own_client(monkeypatch):
    monkeypatch.delenv("OPENAI_API_KEY", raising=False)

    extra = document_host_kwargs("litellm://openai/x", {}, timeout=20.0, send_once=True)

    assert extra == {"timeout": 20.0}


def test_the_startup_probe_is_one_short_attempt(monkeypatch):
    """The question model's client keeps the 60 s timeout and its retries; its
    startup length check waits 20 s once."""
    monkeypatch.setenv("OPENAI_API_KEY", "sk-test")
    hang = _HangingHost()
    monkeypatch.setattr(litellm, "embedding", hang)
    client = _litellm_client(timeout=60.0)

    with pytest.raises(litellm.exceptions.Timeout):
        probe_once(client, "dimension check", timeout=20.0)

    assert len(hang.calls) == 1
    assert hang.calls[0]["timeout"] == 20.0
    assert isinstance(hang.calls[0]["client"], SingleAttemptOpenAI)
