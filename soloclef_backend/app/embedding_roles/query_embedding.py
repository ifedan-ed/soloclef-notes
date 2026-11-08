"""Embed a question or search text: someone is waiting, so it takes no slot.

Every search embeds its query here (chat retrieval, the knowledge-base and
connector searches, notebook search). It goes straight to the question model:
no document slot, no batch, no shared lock with indexing.
"""

from __future__ import annotations

import time

import numpy as np

from app.config import config
from app.observability.domains import embedding


def embed_query(text: str) -> np.ndarray:
    model_name = config.EMBEDDING_QUERY_MODEL
    t0 = time.perf_counter()
    with embedding.embedding_span(
        count=1, model=model_name, extra={"embedding.role": "query"}
    ):
        try:
            vector = config.query_embedding_model_instance.embed(text)
        finally:
            embedding.record_embedding_duration(
                (time.perf_counter() - t0) * 1000, model=model_name, count=1
            )
    if len(vector) != config.EMBEDDING_DIMENSION:
        raise RuntimeError(
            f"Embedding model {model_name} returned a {len(vector)}-dimension "
            f"vector for a question; this server stores {config.EMBEDDING_DIMENSION}"
        )
    return vector
