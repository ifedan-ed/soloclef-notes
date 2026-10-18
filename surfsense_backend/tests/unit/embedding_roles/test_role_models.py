"""The clients built at startup, and the vector length both roles must share."""

from __future__ import annotations

import logging
from types import SimpleNamespace

import pytest

from app.config.embedding_settings import build_embedding_kwargs
from app.embedding_roles.dimension_agreement import EmbeddingDimensionMismatchError
from app.embedding_roles.models import build_role_models, describe_role_models
from app.embedding_roles.settings import load_embedding_role_settings

pytestmark = pytest.mark.unit

LOCAL = "litellm://openai/local-bge-m3"
REMOTE = "litellm://openai/openrouter-bge-m3"
BASE_URL = "http://litellm:4000/v1"


class _Loader:
    """AutoEmbeddings.get_embeddings' place: records each client it makes."""

    def __init__(self, dimensions: dict[str, int]) -> None:
        self.dimensions = dimensions
        self.calls: list[tuple[str, dict]] = []

    def __call__(self, model, **kwargs):
        self.calls.append((model, kwargs))
        return SimpleNamespace(name=model, dimension=self.dimensions.get(model, 1536))


def _host_kwargs(model, kwargs, *, timeout, send_once):
    """clients.document_host_kwargs' place (its own tests are elsewhere)."""
    return {"timeout": timeout, "send_once": send_once}


def _build(environ: dict, dimensions: dict[str, int], measured: dict | None = None):
    loader = _Loader(dimensions)
    settings = load_embedding_role_settings(environ)
    probes: list[str] = []

    def measure(instance):
        probes.append(instance.name)
        value = (dimensions if measured is None else measured).get(instance.name)
        if value is None:
            raise RuntimeError(f"{instance.name} unreachable")
        return value

    models = build_role_models(
        settings,
        load=loader,
        kwargs_for=lambda model: build_embedding_kwargs(environ, embedding_model=model),
        measure=measure,
        host_kwargs=_host_kwargs,
    )
    return settings, models, loader, probes


def test_unset_roles_build_exactly_the_one_client_embedding_model_always_got():
    environ = {"EMBEDDING_MODEL": LOCAL, "EMBEDDING_BASE_URL": BASE_URL}

    _, models, loader, probes = _build(environ, {LOCAL: 1024})

    assert loader.calls == [(LOCAL, {"api_base": BASE_URL})]
    assert loader.calls[0][1] == build_embedding_kwargs(environ, embedding_model=LOCAL)
    assert models.query is models.document
    assert models.agreed.dimension == 1024
    assert probes == [], "one client: no startup probe, nothing new on the wire"


def test_two_hosts_get_two_clients_with_the_same_provider_settings():
    environ = {
        "EMBEDDING_MODEL": LOCAL,
        "EMBEDDING_DOCUMENT_MODEL": REMOTE,
        "EMBEDDING_BASE_URL": BASE_URL,
    }

    settings, models, loader, _ = _build(environ, {LOCAL: 1024, REMOTE: 1024})

    # The document host also gets the short request timeout and, with the
    # fallback on, sends each request once; the question client keeps its own.
    assert sorted(loader.calls) == [
        (LOCAL, {"api_base": BASE_URL}),
        (REMOTE, {"api_base": BASE_URL, "timeout": 20.0, "send_once": True}),
    ]
    assert models.query.name == LOCAL
    assert models.document.name == REMOTE
    assert models.agreed.dimension == 1024
    assert describe_role_models(settings, models) == (
        f"questions {LOCAL}, documents {REMOTE}; 1024 dimensions; "
        "document batches at most 4 at once, 32 texts a request, 20 s timeout; "
        "on the question model's server at most 1 at once, 8 texts a request; "
        "a failed document batch goes to the question model, and so does every "
        "batch for 300 s after"
    )


def test_the_document_timeout_is_the_set_one_and_only_for_a_host_of_its_own():
    environ = {
        "EMBEDDING_MODEL": LOCAL,
        "EMBEDDING_DOCUMENT_MODEL": REMOTE,
        "EMBEDDING_DOCUMENT_TIMEOUT_SECONDS": "7.5",
        "EMBEDDING_DOCUMENT_FALLBACK": "FALSE",
    }
    _, _, loader, _ = _build(environ, {LOCAL: 1024, REMOTE: 1024})
    assert dict(loader.calls)[REMOTE] == {"timeout": 7.5, "send_once": False}
    assert dict(loader.calls)[LOCAL] == {}

    # One model for both: its client also embeds questions, so nothing added.
    _, _, loader, _ = _build({"EMBEDDING_MODEL": REMOTE}, {REMOTE: 1024})
    assert loader.calls == [(REMOTE, {})]

    # A model in this process has no request to time out.
    in_process = "sentence-transformers/all-MiniLM-L6-v2"
    environ = {"EMBEDDING_MODEL": LOCAL, "EMBEDDING_DOCUMENT_MODEL": in_process}
    _, _, loader, _ = _build(environ, {LOCAL: 384, in_process: 384})
    assert dict(loader.calls)[in_process] == {}


def test_two_lengths_refuse_to_start_naming_both_models():
    environ = {"EMBEDDING_QUERY_MODEL": LOCAL, "EMBEDDING_DOCUMENT_MODEL": REMOTE}

    with pytest.raises(EmbeddingDimensionMismatchError) as refused:
        _build(environ, {LOCAL: 1024, REMOTE: 1536})

    message = str(refused.value)
    for part in (
        f"EMBEDDING_QUERY_MODEL ({LOCAL}) gives 1024",
        f"EMBEDDING_DOCUMENT_MODEL ({REMOTE}) gives 1536",
        "search would break",
        "will not start",
    ):
        assert part in message


def test_the_length_is_measured_not_taken_from_the_clients_guess():
    """A client that could not reach its model at construction guesses 1536;
    the probe's answer is what counts."""
    environ = {"EMBEDDING_QUERY_MODEL": LOCAL, "EMBEDDING_DOCUMENT_MODEL": REMOTE}

    _, models, _, probes = _build(environ, {}, measured={LOCAL: 1024, REMOTE: 1024})

    assert sorted(probes) == [LOCAL, REMOTE]
    assert models.agreed.dimension == 1024


def test_a_document_host_down_at_startup_does_not_stop_the_server(caplog):
    environ = {"EMBEDDING_QUERY_MODEL": LOCAL, "EMBEDDING_DOCUMENT_MODEL": REMOTE}

    with caplog.at_level(logging.WARNING):
        settings, models, _, _ = _build(environ, {}, measured={LOCAL: 1024})

    assert models.agreed.dimension == 1024
    assert models.agreed.unverified == ("document",)
    assert "not measured at startup: document" in describe_role_models(settings, models)
    assert any(
        "could not reach the document model" in r.getMessage() for r in caplog.records
    )


def test_neither_host_answering_keeps_the_document_clients_length():
    environ = {"EMBEDDING_QUERY_MODEL": LOCAL, "EMBEDDING_DOCUMENT_MODEL": REMOTE}

    _, models, _, _ = _build(environ, {REMOTE: 1024}, measured={})

    assert models.agreed.dimension == 1024
    assert models.agreed.unverified == ("question", "document")


def test_the_startup_line_for_one_model():
    settings, models, _, _ = _build({"EMBEDDING_MODEL": LOCAL}, {LOCAL: 1024})

    assert describe_role_models(settings, models) == (
        f"questions and documents {LOCAL}; 1024 dimensions; "
        "document batches at most 1 at once, 8 texts a request; "
        "no fallback (one model)"
    )


def test_the_startup_line_for_a_model_in_this_process():
    model = "sentence-transformers/all-MiniLM-L6-v2"
    settings, models, _, _ = _build({"EMBEDDING_MODEL": model}, {model: 384})

    assert describe_role_models(settings, models) == (
        f"questions and documents {model}; 384 dimensions; "
        "document batches in this process, no slot, 8 texts a batch; "
        "no fallback (one model)"
    )
