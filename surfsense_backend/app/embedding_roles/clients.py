"""How a batch is sent to a hosted model: once, never text by text.

Two retries hide inside chonkie's hosted clients. Their ``embed_batch``
answers a failed batch by trying its texts again one at a time (the first
that fails fails the batch), and under ``litellm://openai/...`` the OpenAI
client tries every request three times (litellm turns ``max_retries=0`` into
its default of 2). Against a host that accepts connections and never answers,
measured on 2026-10-12 with a 1 s timeout, a batch of 8 cost 6 connections
and 9.1 s: with the client's 60 s timeout, about 6 minutes inside one slot.

So a document batch is one call (``embed_batch_once``), and a document model
on its own host with a fallback to go to gets a client that sends a request
once (``document_host_kwargs``): a host that hangs costs one timeout, then the
batch goes to the question model and the breaker keeps the next ones away.
"""

from __future__ import annotations

import inspect
import os
from collections.abc import Mapping, Sequence
from typing import Any

import numpy as np
import openai


def embed_batch_once(client: Any, texts: Sequence[str]) -> list[np.ndarray]:
    """``texts``' vectors from one call to ``client``'s host."""
    from chonkie.embeddings.catsu import CatsuEmbeddings
    from chonkie.embeddings.litellm import LiteLLMEmbeddings

    batch = list(texts)
    if isinstance(client, LiteLLMEmbeddings):
        import litellm

        # The same request LiteLLMEmbeddings.embed_batch sends, without its
        # text-by-text retry.
        response = litellm.embedding(input=batch, **client._prepare_api_call_kwargs())
        rows = sorted(response.data, key=lambda row: row["index"])
        return [np.asarray(row["embedding"], dtype=np.float32) for row in rows]
    catsu = getattr(client, "_catsu", client)  # openai://, cohere:// ... wrap one
    if isinstance(catsu, CatsuEmbeddings):
        # As CatsuEmbeddings.embed_batch, without its text-by-text retry.
        response = catsu.client.embed(
            model=catsu.model, input=batch, provider=catsu.provider
        )
        matrix = response.to_numpy()
        return [matrix[i] for i in range(len(batch))]
    return list(client.embed_batch(batch))


def probe_once(client: Any, text: str, *, timeout: float) -> np.ndarray:
    """``text``'s vector from one attempt of at most ``timeout`` seconds: the
    startup length check, so a host that hangs delays a start by ``timeout``
    once, not the client's 60 s three times over."""
    from chonkie.embeddings.litellm import LiteLLMEmbeddings

    if not isinstance(client, LiteLLMEmbeddings):
        return client.embed(text)
    import litellm

    kwargs = client._prepare_api_call_kwargs()
    kwargs["timeout"] = min(timeout, kwargs.get("timeout") or timeout)
    if "client" not in kwargs:
        single = single_attempt_openai_client(
            f"litellm://{client.model}",
            {"api_base": client.api_base, "api_key": client.api_key},
            timeout=kwargs["timeout"],
        )
        if single is not None:
            kwargs["client"] = single
    response = litellm.embedding(input=[text], **kwargs)
    return np.asarray(response.data[0]["embedding"], dtype=np.float32)


def document_host_kwargs(
    model: str | None,
    base_kwargs: Mapping[str, Any],
    *,
    timeout: float,
    send_once: bool,
) -> dict[str, Any]:
    """What a document model on its own host adds to its client's arguments:
    the request ``timeout`` (when its client takes one) and, with
    ``send_once`` (a fallback to go to), no retries inside a request."""
    client_class = _client_class(model)
    if client_class is None:
        return {}
    from chonkie.embeddings.litellm import LiteLLMEmbeddings

    params = _init_parameters(client_class)
    extra: dict[str, Any] = {}
    if "timeout" in params:
        extra["timeout"] = timeout
    if send_once:
        if issubclass(client_class, LiteLLMEmbeddings):
            # chonkie stores unknown arguments and passes them to every
            # litellm.embedding call, its own length probe included.
            single = single_attempt_openai_client(model, base_kwargs, timeout=timeout)
            if single is not None:
                extra["client"] = single
        elif "max_retries" in params:
            extra["max_retries"] = 0
    return extra


class SingleAttemptOpenAI(openai.OpenAI):
    """An OpenAI client that sends each request once. litellm sets
    ``max_retries`` on any client it is handed, to 2 when asked for 0, so
    this one keeps 0 whatever it is told."""

    @property
    def max_retries(self) -> int:  # type: ignore[override]
        return 0

    @max_retries.setter
    def max_retries(self, _value: int) -> None:
        pass


def single_attempt_openai_client(
    model: str | None, base_kwargs: Mapping[str, Any], *, timeout: float
) -> SingleAttemptOpenAI | None:
    """For ``litellm://openai/<name>``: the client litellm would build (same
    address and key, found the way litellm finds them), sending once. None
    for another litellm provider, or with no key to give it (litellm then
    builds its own, as before)."""
    name = (model or "").split("://", 1)[-1]
    if not name.startswith("openai/"):
        return None
    api_key = base_kwargs.get("api_key") or os.environ.get("OPENAI_API_KEY")
    if not api_key:
        return None
    base_url = (
        base_kwargs.get("api_base")
        or os.environ.get("OPENAI_BASE_URL")
        or os.environ.get("OPENAI_API_BASE")
        or "https://api.openai.com/v1"
    )
    return SingleAttemptOpenAI(api_key=api_key, base_url=base_url, timeout=timeout)


def _client_class(model: str | None):
    if not model or "://" not in model:
        return None
    from chonkie.embeddings.registry import EmbeddingsRegistry

    # An unknown provider: None here, and it fails where it always has.
    return EmbeddingsRegistry.get_provider(model.split("://", 1)[0])


def _init_parameters(client_class) -> Mapping[str, inspect.Parameter]:
    try:
        return inspect.signature(client_class.__init__).parameters
    except (TypeError, ValueError):
        return {}
