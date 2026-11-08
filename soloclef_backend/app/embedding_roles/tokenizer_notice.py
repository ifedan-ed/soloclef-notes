"""Chonkie's guess at the embedding model's tokenizer, reported accurately.

``LiteLLMEmbeddings`` warns at construction that it could not map the model
name to a tiktoken encoding and is "Using cl100k_base fallback", which reads
like every chunk is being sized by the wrong vocabulary. It is not: nothing
in this server asks that client to count anything, and what sizes a chunk is
``app/config/chunk_sizing.py``. The record is rewritten rather than silenced
so a start still names the setting that decides.
"""

from __future__ import annotations

import logging

from app.config.chunk_sizing import TOKENIZER_ENV

CHONKIE_LITELLM_LOGGER = "chonkie.embeddings.litellm"

_GUESS = "Failed to initialize tokenizer"


class _TokenizerGuessNotice(logging.Filter):
    """Rewrites the one record; every other warning from that client passes."""

    def filter(self, record: logging.LogRecord) -> bool:
        if not str(record.msg).startswith(_GUESS):
            return True
        record.msg = (
            "The embedding client could not name its model's tokenizer and has "
            "picked one for its own token counting, which this server never "
            "uses. Chunk sizes come from %s (unset: characters)."
        )
        record.args = (TOKENIZER_ENV,)
        record.exc_info = None
        return True


def say_what_sizes_a_chunk(logger_name: str = CHONKIE_LITELLM_LOGGER) -> None:
    """Install the rewrite before any embedding client is built."""
    logger = logging.getLogger(logger_name)
    if any(isinstance(f, _TokenizerGuessNotice) for f in logger.filters):
        return
    logger.addFilter(_TokenizerGuessNotice())
