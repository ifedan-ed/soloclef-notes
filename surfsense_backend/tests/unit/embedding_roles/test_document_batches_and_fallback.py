"""Stored texts go in batches sized for the server they go to; a failed batch
falls back to the question model, once, and the breaker keeps the rest of an
import off a failing host for a while."""

from __future__ import annotations

import logging
import time

import numpy as np
import pytest
from celery.exceptions import SoftTimeLimitExceeded

from app.config import config
from app.embedding_roles import breaker
from app.embedding_roles.document_embedding import embed_documents
from app.utils.document_converters import embed_text, embed_texts

from .conftest import DOCUMENT_NAME

pytestmark = pytest.mark.unit

BREAKER_KEY = breaker.breaker_key(DOCUMENT_NAME)


def _lengths(model) -> list[int]:
    return [len(request) for request in model.requests]


def test_a_document_host_of_its_own_gets_batches_of_its_size(roles):
    texts = [f"chunk {i}" for i in range(70)]

    vectors = embed_texts(texts)

    assert _lengths(roles.document) == [32, 32, 6]
    assert roles.document.texts == texts
    # Order kept across batches: each fake vector is the text's length.
    assert [float(v[0]) for v in vectors] == [float(len(t)) for t in texts]
    assert roles.query.requests == []


def test_one_model_for_both_gets_the_question_servers_small_batches(roles, monkeypatch):
    monkeypatch.setattr(config, "query_embedding_model_instance", roles.document)
    monkeypatch.setattr(config, "EMBEDDING_QUERY_MODEL", DOCUMENT_NAME)

    embed_texts([f"chunk {i}" for i in range(20)])

    assert _lengths(roles.document) == [8, 8, 4]


def test_one_call_sends_its_batches_side_by_side_up_to_the_cap(roles, monkeypatch):
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_CONCURRENCY", 4)
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_BATCH_SIZE", 2)
    roles.document.delay = 0.05
    texts = [f"chunk {i:02d}" for i in range(16)]

    started = time.monotonic()
    vectors = embed_texts(texts)
    took = time.monotonic() - started

    assert roles.document.peak == 4
    assert roles.redis.peak[f"embedding:document_slot:{DOCUMENT_NAME}"] == 4
    assert sorted(roles.document.texts) == texts
    assert [float(v[0]) for v in vectors] == [float(len(t)) for t in texts]
    assert took < 8 * 0.05, "eight batches in about two rounds, not eight"


def test_a_failed_batch_is_embedded_with_the_question_model_and_logged(roles, caplog):
    roles.document.fail_next = 1

    with caplog.at_level(logging.WARNING):
        vectors = embed_texts(["a chunk", "another"])

    assert roles.query.texts == ["a chunk", "another"]
    assert len(vectors) == 2
    line = next(
        r.getMessage() for r in caplog.records if "failed for a batch" in r.getMessage()
    )
    assert "litellm://openai/document-host" in line
    assert "litellm://openai/question-host" in line
    assert "every batch for the next 300s" in line


def test_a_fallback_batch_goes_in_the_question_servers_batch_size(roles):
    roles.document.fail_next = 1

    embed_texts([f"chunk {i}" for i in range(20)])

    assert _lengths(roles.document) == [20]
    assert _lengths(roles.query) == [8, 8, 4]


def test_after_a_failure_batches_skip_the_document_model_until_the_breaker_closes(
    roles, monkeypatch
):
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_BATCH_SIZE", 2)
    roles.document.fail_next = 1

    embed_texts(["one", "two", "three", "four"])
    embed_text("a note")

    assert roles.document.requests == [["one", "two"]], "tried once, then skipped"
    assert roles.query.texts == ["one", "two", "three", "four", "a note"]
    assert roles.redis.exists(BREAKER_KEY)

    roles.redis.free(BREAKER_KEY)  # the 300 s are up (or someone deleted it)
    embed_text("later")

    assert roles.document.texts[-1] == "later"


def test_the_breaker_lasts_the_set_time(roles, monkeypatch):
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_BREAKER_SECONDS", 0.2)
    roles.document.fail_next = 1

    embed_text("fails")
    embed_text("skips")
    time.sleep(0.3)
    embed_text("tried again")

    assert roles.document.texts == ["fails", "tried again"]
    assert roles.query.texts == ["fails", "skips"]


def test_a_breaker_another_process_opened_is_honoured(roles):
    roles.redis.set(BREAKER_KEY, "OpenRouter timed out", px=60_000)

    embed_texts(["a chunk"])

    assert roles.document.requests == []
    assert roles.query.texts == ["a chunk"]


def test_without_redis_the_breaker_holds_in_this_process(roles, monkeypatch):
    from redis.exceptions import ConnectionError as RedisConnectionError

    from app.embedding_roles import redis_link

    def unreachable():
        raise RedisConnectionError("Redis is down")

    monkeypatch.setattr(redis_link, "redis_client", unreachable)
    roles.document.fail_next = 1

    embed_text("fails")
    embed_text("skips")

    assert roles.document.texts == ["fails"]
    assert roles.query.texts == ["fails", "skips"]


def test_a_breaker_of_zero_tries_the_document_model_every_batch(roles, monkeypatch):
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_BREAKER_SECONDS", 0.0)
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_BATCH_SIZE", 2)
    roles.document.fail_next = 1

    embed_texts(["one", "two", "three", "four"])

    assert roles.query.texts == ["one", "two"]
    assert roles.document.texts == ["one", "two", "three", "four"]
    assert not roles.redis.exists(BREAKER_KEY)


def test_with_the_fallback_off_a_failed_batch_fails_and_the_breaker_is_ignored(
    roles, monkeypatch
):
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_FALLBACK", False)
    roles.redis.set(BREAKER_KEY, "opened earlier", px=60_000)
    roles.document.fail_next = 1

    with pytest.raises(RuntimeError, match="document-host is down"):
        embed_texts(["a chunk"])
    embed_texts(["the next one"])

    assert roles.query.requests == []
    assert roles.document.texts == ["a chunk", "the next one"]


def test_one_model_for_both_roles_has_nothing_to_fall_back_to(roles, monkeypatch):
    monkeypatch.setattr(config, "query_embedding_model_instance", roles.document)
    roles.document.fail_next = 1

    with pytest.raises(RuntimeError, match="document-host is down"):
        embed_text("a note")
    assert len(roles.document.requests) == 1


def test_a_task_out_of_time_is_not_a_model_failure(roles, monkeypatch):
    def out_of_time(_texts):
        raise SoftTimeLimitExceeded()

    monkeypatch.setattr(roles.document, "embed_batch", out_of_time)

    with pytest.raises(SoftTimeLimitExceeded):
        embed_documents(["a chunk"])
    assert roles.query.requests == []
    assert not roles.redis.exists(BREAKER_KEY)


def test_vectors_of_another_length_are_never_stored(roles, monkeypatch):
    """A document model answering in another length falls back; a fallback
    answering in another length fails the batch rather than store it."""
    monkeypatch.setattr(config, "EMBEDDING_DIMENSION", 4)
    roles.document.dimension = 6

    assert len(embed_text("a note")) == 4
    assert roles.query.texts == ["a note"]

    roles.query.dimension = 6
    with pytest.raises(RuntimeError, match="6-dimension vectors; this server stores 4"):
        embed_text("another note")


def test_a_model_in_this_process_embeds_one_text_at_a_time(roles, monkeypatch):
    monkeypatch.setattr(
        config, "EMBEDDING_DOCUMENT_MODEL", "sentence-transformers/all-MiniLM-L6-v2"
    )

    embed_texts(["one", "two", "three"])

    assert roles.document.requests == [["one"], ["two"], ["three"]]


def test_the_question_path_checks_the_length_too(roles, monkeypatch):
    from app.embedding_roles.query_embedding import embed_query

    assert isinstance(embed_query("a question"), np.ndarray)
    roles.query.dimension = 6
    with pytest.raises(RuntimeError, match="6-dimension vector for a question"):
        embed_query("a question")


def test_batches_waiting_for_a_slot_when_the_breaker_trips_skip_the_failing_host(
    roles, monkeypatch
):
    # More batches than slots, all at once (several documents, or processes):
    # the first ones fail slowly and trip the breaker while the rest wait for a
    # slot. Those must not go to the failing host once they get one, or a hung
    # host costs a round of timeouts per waiting group instead of one in all.
    from concurrent.futures import ThreadPoolExecutor

    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_CONCURRENCY", 2)
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_BATCH_SIZE", 1)
    roles.document.delay = 0.1
    roles.document.fail_next = 100  # the host fails everything it is sent

    texts = [f"document {i}" for i in range(6)]
    with ThreadPoolExecutor(max_workers=6) as pool:
        answers = list(pool.map(lambda t: embed_texts([t]), texts))

    assert len(roles.document.requests) <= 2, "one round on the failing host, not three"
    assert sorted(roles.query.texts) == sorted(texts), "every batch reached the fallback"
    assert all(len(a) == 1 for a in answers)
    assert not any(roles.redis.held(f"embedding:document_slot:{DOCUMENT_NAME}"))
