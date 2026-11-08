"""Chonkie's cl100k_base warning, replaced by what actually sizes a chunk."""

from __future__ import annotations

import logging

import pytest

from app.config.chunk_sizing import TOKENIZER_ENV
from app.embedding_roles.tokenizer_notice import (
    CHONKIE_LITELLM_LOGGER,
    say_what_sizes_a_chunk,
)

pytestmark = pytest.mark.unit

GUESS = (
    "Failed to initialize tokenizer: 'Could not automatically map "
    "cloudflare-bge-m3 to a tokeniser.'. Using cl100k_base fallback."
)


class _Collect(logging.Handler):
    def __init__(self) -> None:
        super().__init__()
        self.records: list[logging.LogRecord] = []

    def emit(self, record: logging.LogRecord) -> None:
        self.records.append(record)


@pytest.fixture
def chonkie_logger():
    """The client's own logger, which chonkie keeps off the root handlers."""
    logger = logging.getLogger(CHONKIE_LITELLM_LOGGER)
    kept_filters, kept_level = list(logger.filters), logger.level
    collector = _Collect()
    logger.addHandler(collector)
    logger.setLevel(logging.WARNING)
    yield logger, collector
    logger.removeHandler(collector)
    logger.filters = kept_filters
    logger.setLevel(kept_level)


def test_the_guess_is_replaced_by_the_setting_that_decides(chonkie_logger):
    logger, collected = chonkie_logger
    say_what_sizes_a_chunk()

    logger.warning(GUESS, exc_info=False)

    said = collected.records[0].getMessage()
    assert TOKENIZER_ENV in said
    assert "cl100k_base" not in said
    # Still a warning: a start that says nothing is how this went unnoticed.
    assert collected.records[0].levelno == logging.WARNING


def test_the_clients_other_warnings_are_left_alone(chonkie_logger):
    logger, collected = chonkie_logger
    say_what_sizes_a_chunk()

    logger.warning("Failed to auto-detect embedding dimension: %s", "boom")

    assert (
        collected.records[0].getMessage()
        == "Failed to auto-detect embedding dimension: boom"
    )


def test_installing_it_again_does_not_stack_filters(chonkie_logger):
    logger, _ = chonkie_logger
    say_what_sizes_a_chunk()
    say_what_sizes_a_chunk()

    assert len(logger.filters) == 1
