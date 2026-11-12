"""The pool's promises: a bad file costs one request, and the pool recovers."""

import threading

import pytest

from parse_service.workers import Busy, WorkerPool
from tests import converters


def pool_of(convert, *, size=1, parse_timeout=30.0, queue_wait=5.0):
    return WorkerPool(
        size=size,
        convert=convert,
        parse_timeout_seconds=parse_timeout,
        queue_wait_seconds=queue_wait,
    )


def test_a_worker_answers_and_is_reused():
    pool = pool_of(converters.echo)
    try:
        assert pool.parse("a")["pages"][0]["markdown"] == "a"
        assert pool.parse("b")["pages"][0]["markdown"] == "b"
        assert pool.idle() == 1
    finally:
        pool.close()


def test_a_worker_that_dies_refuses_that_file_and_is_replaced():
    pool = pool_of(converters.die_on_bad)
    try:
        result = pool.parse("bad")
        assert result["refusal"]["kind"] == "parse_error"
        assert "died" in result["refusal"]["message"]
        # The replacement serves the next file.
        assert pool.alive() == 1
        assert pool.parse("good")["pages"][0]["markdown"] == "good"
    finally:
        pool.close()


def test_a_parse_past_the_time_limit_is_killed_and_refused():
    pool = pool_of(converters.hang, parse_timeout=0.3)
    try:
        result = pool.parse("a")
        assert result["refusal"]["kind"] == "parse_error"
        assert "0.3 seconds" in result["refusal"]["message"]
        assert pool.alive() == 1
    finally:
        pool.close()


def test_a_request_that_finds_no_free_worker_is_told_busy():
    pool = pool_of(converters.slow, queue_wait=0.1)
    try:
        first = threading.Thread(target=pool.parse, args=("a",))
        first.start()
        # Let the first request take the only worker.
        while pool.idle():
            pass
        with pytest.raises(Busy):
            pool.parse("b")
        first.join()
    finally:
        pool.close()
