"""Questions and documents must be embedded into the same space.

A question's vector is compared with stored document vectors, so the two
models must give vectors of one length (and, for the comparison to mean
anything, be one model: two hosts of bge-m3, say). A different length is
caught here, at startup, and the server refuses to start rather than search
nonsense. Two different models of the same length cannot be told apart by
length; the docs say to use two hosts of one model.
"""

from __future__ import annotations

import logging
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from app.embedding_roles.clients import probe_once

logger = logging.getLogger(__name__)

# The text embedded to measure a model's vector length at startup.
PROBE_TEXT = "dimension check"
# How long the check waits for each model, once (no retries): a model that
# hangs delays a start by this, then the start goes on without its length.
PROBE_TIMEOUT_SECONDS = 20.0


class EmbeddingDimensionMismatchError(ValueError):
    """The question model and the document model give vectors of different lengths."""


@dataclass(frozen=True, slots=True)
class AgreedDimension:
    dimension: int
    # The roles whose length could not be measured at startup (the model was
    # unreachable); their vectors are still checked against ``dimension``.
    unverified: tuple[str, ...] = ()


def agree_on_dimension(
    *,
    query_model: str | None,
    query_instance: Any,
    document_model: str | None,
    document_instance: Any,
    measure: Callable[[Any], int] | None = None,
) -> AgreedDimension:
    """The vector length both roles share, or refuse to start.

    One shared instance needs no check and keeps today's behaviour exactly:
    its own ``dimension``. Two instances are each asked to embed a probe; a
    model that cannot be reached at startup is not a reason to stop (the
    other's length is used, and that role's vectors are checked as they
    arrive), but two measured lengths that differ are.
    """
    if query_instance is document_instance:
        return AgreedDimension(int(document_instance.dimension))

    measure = measure or _measured_length
    query_dim = _try_measure(measure, query_instance, "question", query_model)
    document_dim = _try_measure(measure, document_instance, "document", document_model)

    if query_dim is not None and document_dim is not None:
        if query_dim != document_dim:
            raise EmbeddingDimensionMismatchError(
                f"EMBEDDING_QUERY_MODEL ({query_model}) gives {query_dim}-dimension "
                f"vectors and EMBEDDING_DOCUMENT_MODEL ({document_model}) gives "
                f"{document_dim}: questions would be compared with documents of "
                "another length and search would break. Use two hosts of the same "
                "model, or remove both lines to use EMBEDDING_MODEL for both. "
                "The server will not start until they agree."
            )
        return AgreedDimension(document_dim)
    if document_dim is not None:
        return AgreedDimension(document_dim, unverified=("question",))
    if query_dim is not None:
        return AgreedDimension(query_dim, unverified=("document",))
    # Neither answered: what the document model's client says, as before.
    return AgreedDimension(
        int(document_instance.dimension), unverified=("question", "document")
    )


def _measured_length(instance: Any) -> int:
    return len(probe_once(instance, PROBE_TEXT, timeout=PROBE_TIMEOUT_SECONDS))


def _try_measure(
    measure: Callable[[Any], int], instance: Any, role: str, model: str | None
) -> int | None:
    try:
        return int(measure(instance))
    except Exception as exc:
        logger.warning(
            "[startup] embeddings: could not reach the %s model %s to check its "
            "vector length (%s); its vectors are checked as they arrive instead",
            role,
            model,
            exc,
        )
        return None
