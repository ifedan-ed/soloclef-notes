"""The two embedding clients, built once at startup from the role settings."""

from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Any

from app.embedding_roles.clients import document_host_kwargs
from app.embedding_roles.dimension_agreement import AgreedDimension, agree_on_dimension
from app.embedding_roles.settings import EmbeddingRoleSettings, runs_in_process

# (model name, keyword arguments) -> client; Chonkie's AutoEmbeddings in use.
Loader = Callable[..., Any]
# model name -> keyword arguments for its client (base URL, Azure keys).
KwargsFor = Callable[[str | None], Mapping[str, Any]]
# (model name, its keyword arguments, timeout=, send_once=) -> what a document
# model on its own host adds to them (clients.document_host_kwargs).
HostKwargs = Callable[..., Mapping[str, Any]]


@dataclass(frozen=True, slots=True)
class RoleModels:
    query: Any
    document: Any
    agreed: AgreedDimension

    @property
    def shared(self) -> bool:
        return self.query is self.document


def build_role_models(
    settings: EmbeddingRoleSettings,
    *,
    load: Loader,
    kwargs_for: KwargsFor,
    measure: Callable[[Any], int] | None = None,
    host_kwargs: HostKwargs = document_host_kwargs,
) -> RoleModels:
    """One client when both roles name one model (today's setup), else two.

    Each client gets the same provider settings ``EMBEDDING_MODEL`` always
    got (``EMBEDDING_BASE_URL`` for a ``litellm://`` model, the Azure keys),
    so a role differs from the other only by the model it names. A document
    model on its own host also gets ``EMBEDDING_DOCUMENT_TIMEOUT_SECONDS`` as
    its request timeout and, with the fallback on, a client that sends each
    request once (``clients``), so a host that stops answering costs one
    timeout, not the client's 60 s three times over; its client's own length
    probe at construction is bounded the same way.
    """
    document_kwargs = dict(kwargs_for(settings.document_model))
    if settings.document_has_own_host:
        document_kwargs.update(
            host_kwargs(
                settings.document_model,
                document_kwargs,
                timeout=settings.document_timeout_seconds,
                send_once=settings.document_fallback,
            )
        )
    document = load(settings.document_model, **document_kwargs)
    if settings.one_model:
        query = document
    else:
        query = load(settings.query_model, **kwargs_for(settings.query_model))
    agreed = agree_on_dimension(
        query_model=settings.query_model,
        query_instance=query,
        document_model=settings.document_model,
        document_instance=document,
        measure=measure,
    )
    return RoleModels(query=query, document=document, agreed=agreed)


def describe_role_models(settings: EmbeddingRoleSettings, models: RoleModels) -> str:
    """The startup line: which model embeds what, the length, the limits."""
    if models.shared:
        who = f"questions and documents {settings.document_model}"
    else:
        who = f"questions {settings.query_model}, documents {settings.document_model}"
    unverified = (
        f" (not measured at startup: {', '.join(models.agreed.unverified)})"
        if models.agreed.unverified
        else ""
    )
    query_host = _limits(
        settings.query_model,
        settings.query_host_concurrency,
        settings.query_host_batch_size,
    )
    if models.shared:
        where = f"document batches {query_host}"
        fallback = "no fallback (one model)"
    else:
        own = _limits(
            settings.document_model,
            settings.document_concurrency,
            settings.document_batch_size,
        )
        if settings.document_has_own_host:
            own += f", {settings.document_timeout_seconds:g} s timeout"
        where = f"document batches {own}; on the question model's server {query_host}"
        if not settings.document_fallback:
            fallback = "no fallback (EMBEDDING_DOCUMENT_FALLBACK=FALSE)"
        elif settings.document_breaker_seconds > 0:
            fallback = (
                "a failed document batch goes to the question model, and so does "
                f"every batch for {settings.document_breaker_seconds:g} s after"
            )
        else:
            fallback = "a failed document batch goes to the question model"
    return (
        f"{who}; {models.agreed.dimension} dimensions{unverified}; {where}; {fallback}"
    )


def _limits(model: str | None, concurrency: int, batch_size: int) -> str:
    if runs_in_process(model):
        return f"in this process, no slot, {batch_size} texts a batch"
    cap = "no cap" if concurrency == 0 else f"at most {concurrency} at once"
    return f"{cap}, {batch_size} texts a request"
