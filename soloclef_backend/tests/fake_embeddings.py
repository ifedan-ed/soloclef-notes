"""An embedding host that is not a host: ``fake-embeddings://<dimension>``.

``app.config`` builds an embedding client while it is being imported, so every
test run (and the offline E2E stack) needs *something* for ``EMBEDDING_MODEL``
to name. It used to name ``sentence-transformers/all-MiniLM-L6-v2``, which
chonkie loaded into the test process: a real model, a real download, and the
only reason the backend depended on sentence-transformers at all.

This stands in its place. It is registered with chonkie under its own provider
scheme, so the name carries a provider like every other one the server accepts
and nothing has to make an exception for it:

    EMBEDDING_MODEL=fake-embeddings://384

Vectors are derived from the text with BLAKE2b, so they are deterministic
across processes and runs (a test can embed the same text twice and compare),
unit length, and meaningless -- nothing here measures similarity for real.

``register()`` is idempotent and must run before ``app.config`` is imported.
"""

from __future__ import annotations

import hashlib

import numpy as np
from chonkie.embeddings.base import BaseEmbeddings
from chonkie.embeddings.registry import EmbeddingsRegistry
from chonkie.tokenizer import AutoTokenizer

PROVIDER = "fake-embeddings"
DEFAULT_DIMENSION = 384
#: What a test (or the E2E stack) sets ``EMBEDDING_MODEL`` to.
MODEL_NAME = f"{PROVIDER}://{DEFAULT_DIMENSION}"


class FakeEmbeddings(BaseEmbeddings):
    """Deterministic vectors of a stated length, with no model behind them."""

    def __init__(self, model: str = str(DEFAULT_DIMENSION), **_kwargs: object) -> None:
        super().__init__()
        try:
            self._dimension = int(model)
        except ValueError as exc:
            raise ValueError(
                f"{PROVIDER}://{model} does not name a vector length; use "
                f"{PROVIDER}://<whole number>, for example {MODEL_NAME}."
            ) from exc
        if self._dimension < 1:
            raise ValueError(f"{PROVIDER}://{model} is not a length of at least 1")
        self._tokenizer = AutoTokenizer("character")

    @property
    def dimension(self) -> int:
        return self._dimension

    def embed(self, text: str) -> np.ndarray:
        """``text``'s vector: BLAKE2b bytes of it, scaled to unit length."""
        wanted = self._dimension
        digest = b""
        block = 0
        while len(digest) < wanted:
            digest += hashlib.blake2b(
                text.encode("utf-8"), digest_size=64, salt=str(block).encode()[:16]
            ).digest()
            block += 1
        vector = np.frombuffer(digest[:wanted], dtype=np.uint8).astype(np.float32)
        vector -= 127.5
        norm = float(np.linalg.norm(vector)) or 1.0
        return vector / norm

    def embed_batch(self, texts: list[str]) -> list[np.ndarray]:
        return [self.embed(text) for text in texts]

    def get_tokenizer(self):
        """Chonkie's character counter: no vocabulary to fetch."""
        return self._tokenizer

    def __repr__(self) -> str:
        return f"FakeEmbeddings({PROVIDER}://{self._dimension})"


def register() -> None:
    """Teach chonkie the ``fake-embeddings://`` scheme. Safe to call twice."""
    if EmbeddingsRegistry.get_provider(PROVIDER) is not FakeEmbeddings:
        EmbeddingsRegistry.register_provider(PROVIDER, FakeEmbeddings)
