"""Each server documents go to has its own host-wide cap; a question never
waits for a slot; a model in this process takes none."""

from __future__ import annotations

import threading
import time

import pytest
from redis.exceptions import ConnectionError as RedisConnectionError

from app.config import config
from app.embedding_roles import document_slots, redis_link
from app.embedding_roles.document_embedding import embed_document_batch
from app.embedding_roles.query_embedding import embed_query
from app.embedding_roles.redis_link import redis_client as real_redis_client
from app.utils.document_converters import embed_texts

from .conftest import DOCUMENT_NAME, QUERY_NAME

pytestmark = pytest.mark.unit

DOCUMENT_POOL = f"{document_slots.SLOT_KEY_PREFIX}:{DOCUMENT_NAME}"
QUERY_POOL = f"{document_slots.SLOT_KEY_PREFIX}:{QUERY_NAME}"


@pytest.fixture(autouse=True)
def _quick_polls(monkeypatch):
    monkeypatch.setattr(document_slots, "_POLL_SECONDS", 0.01)


def _in_thread(fn, *args) -> threading.Thread:
    thread = threading.Thread(target=fn, args=args, daemon=True)
    thread.start()
    return thread


def _slots_held(roles) -> set[str]:
    return roles.redis.held(document_slots.SLOT_KEY_PREFIX)


def test_a_question_is_answered_while_every_document_slot_is_taken(roles):
    # Other processes hold every slot of both servers: an import in flight.
    roles.redis.hold(f"{DOCUMENT_POOL}:0")
    roles.redis.hold(f"{QUERY_POOL}:0")

    started = time.monotonic()
    embed_query("what did the flood destroy?")

    assert time.monotonic() - started < 1
    assert roles.query.texts == ["what did the flood destroy?"]


def test_a_document_waits_for_a_slot_then_is_embedded(roles):
    roles.redis.hold(f"{DOCUMENT_POOL}:0")

    worker = _in_thread(embed_texts, ["a chunk"])
    time.sleep(0.2)
    assert roles.document.requests == [], "no request while the slot is taken"

    roles.redis.free(f"{DOCUMENT_POOL}:0")  # the other process's batch is done
    worker.join(timeout=5)

    assert roles.document.texts == ["a chunk"]
    assert _slots_held(roles) == set(), "the slot is given back"


def test_documents_from_many_processes_never_exceed_their_servers_cap(
    roles, monkeypatch
):
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_CONCURRENCY", 2)
    roles.document.delay = 0.02
    # Each thread stands for another process, so they call the batch embedder
    # directly: only the shared slots limit them.
    workers = [_in_thread(embed_document_batch, [f"t{i}"]) for i in range(6)]
    for worker in workers:
        worker.join(timeout=5)

    assert len(roles.document.texts) == 6
    assert roles.document.peak == 2
    assert roles.redis.peak[DOCUMENT_POOL] == 2


def test_fallback_batches_take_the_question_servers_one_slot(roles, monkeypatch):
    """Four document batches fail at once; their fallbacks reach the question
    model's server one at a time, so it never has two document batches."""
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_CONCURRENCY", 4)
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_BREAKER_SECONDS", 0.0)
    roles.document.fail_next = 4
    roles.document.delay = 0.05
    roles.query.delay = 0.02

    workers = [_in_thread(embed_document_batch, [f"t{i}"]) for i in range(4)]
    for worker in workers:
        worker.join(timeout=5)

    assert sorted(roles.query.texts) == ["t0", "t1", "t2", "t3"]
    assert roles.document.peak == 4
    assert roles.query.peak == 1
    assert roles.redis.peak[QUERY_POOL] == 1


def test_the_document_slot_is_given_back_before_the_fallback_takes_its_own(roles):
    roles.document.fail_next = 1
    held_during_fallback: list[set[str]] = []
    answer = roles.query.embed_batch

    def watching(texts):
        held_during_fallback.append(_slots_held(roles))
        return answer(texts)

    roles.query.embed_batch = watching

    embed_texts(["a chunk"])

    assert held_during_fallback == [{f"{QUERY_POOL}:0"}]
    assert _slots_held(roles) == set()


def test_a_failed_request_gives_its_slot_back(roles, monkeypatch):
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_FALLBACK", False)
    roles.document.fail_next = 1

    with pytest.raises(RuntimeError):
        embed_texts(["a chunk"])

    assert _slots_held(roles) == set()


def test_a_long_request_keeps_its_slot_by_renewing_the_lease(roles, monkeypatch):
    monkeypatch.setattr(document_slots, "SLOT_RENEW_SECONDS", 0.02)
    roles.document.delay = 0.15

    embed_texts(["a chunk"])
    renewed = roles.redis.renewals
    time.sleep(0.1)

    assert roles.redis.leases == [document_slots.SLOT_LEASE_SECONDS]
    assert renewed >= 3, "renewed while the request ran"
    assert roles.redis.renewals == renewed, "and not after it was given back"


def test_the_lease_outlasts_renewals_that_each_wait_out_redis():
    # Three renewals a lease; two may each take the whole Redis timeout.
    assert document_slots.SLOT_RENEW_SECONDS * 3 <= document_slots.SLOT_LEASE_SECONDS
    assert (
        document_slots.SLOT_RENEW_SECONDS + 2 * redis_link.REDIS_TIMEOUT_SECONDS
        < document_slots.SLOT_LEASE_SECONDS
    )


class _DownRedis:
    """A Redis client whose every call fails, counting them."""

    def __init__(self) -> None:
        self.calls = 0

    def _fail(self, *_args, **_kwargs):
        self.calls += 1
        raise RedisConnectionError("Redis is down")

    exists = set = lock = _fail


def test_without_redis_documents_are_embedded_and_redis_is_left_alone(
    roles, monkeypatch
):
    down = _DownRedis()
    monkeypatch.setattr(redis_link, "redis_client", real_redis_client)
    monkeypatch.setattr(redis_link, "_client", down)

    embed_texts(["a chunk"])
    embed_texts(["another chunk"])

    assert roles.document.texts == ["a chunk", "another chunk"]
    assert down.calls == 1, "after one failure Redis is left alone for a minute"
    assert redis_link._down_until > time.monotonic() + 50


def test_no_free_slot_within_the_wait_embeds_anyway(roles):
    roles.redis.hold(f"{DOCUMENT_POOL}:0")

    with document_slots.document_embedding_slot(DOCUMENT_NAME, 1, wait=0.05):
        pass  # reached without the slot

    assert _slots_held(roles) == {f"{DOCUMENT_POOL}:0"}


def test_a_cap_of_zero_takes_no_slot(roles, monkeypatch):
    def must_not_be_called(*_args, **_kwargs):
        raise AssertionError("no slot without a cap")

    monkeypatch.setattr(roles.redis, "lock", must_not_be_called)
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_CONCURRENCY", 0)

    embed_texts(["a chunk"])

    assert roles.document.texts == ["a chunk"]


@pytest.mark.parametrize("one_model", [True, False])
def test_a_model_in_this_process_takes_no_slot(roles, monkeypatch, one_model):
    """sentence-transformers/all-MiniLM-L6-v2: every process has its own copy,
    so there is nothing to share and no slot is asked for."""
    local = "sentence-transformers/all-MiniLM-L6-v2"

    def must_not_be_called(*_args, **_kwargs):
        raise AssertionError("no slot for a model in this process")

    monkeypatch.setattr(roles.redis, "lock", must_not_be_called)
    monkeypatch.setattr(config, "EMBEDDING_DOCUMENT_MODEL", local)
    if one_model:
        monkeypatch.setattr(config, "EMBEDDING_QUERY_MODEL", local)
        monkeypatch.setattr(config, "query_embedding_model_instance", roles.document)

    embed_texts(["one", "two"])

    assert roles.document.requests == [["one"], ["two"]]
