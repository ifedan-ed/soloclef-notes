"""The dedupe keys: what they are made of, and what is deliberately not in them."""

from __future__ import annotations

import pytest

from app.mail_queue.dedupe import keys

pytestmark = pytest.mark.unit


def test_a_callers_key_is_hashed_so_no_token_becomes_a_redis_key_name():
    key = keys.queued_key("password_reset:tok-secret-123")
    assert "tok-secret-123" not in key
    assert key.startswith("soloclef:mail:queued:")


def test_the_same_callers_key_is_the_same_redis_key():
    assert keys.queued_key("invite:7:1762608000.0") == keys.queued_key(
        "invite:7:1762608000.0"
    )
    assert keys.queued_key("invite:7:1762608000.0") != keys.queued_key(
        "invite:7:1762608060.0"
    )


def test_the_handover_key_carries_the_attempt_so_a_retry_is_not_a_duplicate():
    first = keys.handover_key("aaaabbbbccccddddeeeeffff00001111", 0)
    retry = keys.handover_key("aaaabbbbccccddddeeeeffff00001111", 1)
    assert first != retry
    assert first == "soloclef:mail:handover:aaaabbbbccccddddeeeeffff00001111:0"


def test_the_window_is_a_day_and_the_same_number_for_both_guards():
    assert keys.WINDOW_SECONDS == 86_400
