"""Refusing the second push of a mail already queued."""

from __future__ import annotations

import pytest

from app.mail_queue.dedupe import keys
from app.mail_queue.dedupe.queued_once import QueuedOnce

pytestmark = pytest.mark.unit

KEY = "password_reset:abc"


async def test_the_first_push_takes_the_key(fake_redis):
    assert await QueuedOnce(fake_redis).claim(KEY, "mail-1") is None


async def test_the_second_push_is_told_which_mail_already_holds_it(fake_redis):
    ledger = QueuedOnce(fake_redis)
    await ledger.claim(KEY, "mail-1")
    assert await ledger.claim(KEY, "mail-2") == "mail-1"


async def test_a_different_key_is_a_different_mail(fake_redis):
    ledger = QueuedOnce(fake_redis)
    await ledger.claim(KEY, "mail-1")
    assert await ledger.claim("password_reset:def", "mail-2") is None


async def test_once_the_window_passes_the_same_mail_may_be_queued_again(fake_redis):
    ledger = QueuedOnce(fake_redis)
    await ledger.claim(KEY, "mail-1")
    fake_redis.now += keys.WINDOW_SECONDS + 1
    assert await ledger.claim(KEY, "mail-2") is None


async def test_a_push_that_failed_gives_the_key_back(fake_redis):
    ledger = QueuedOnce(fake_redis)
    await ledger.claim(KEY, "mail-1")
    await ledger.release(KEY, "mail-1")
    assert await ledger.claim(KEY, "mail-2") is None


async def test_releasing_does_not_take_the_key_from_another_mail(fake_redis):
    ledger = QueuedOnce(fake_redis)
    await ledger.claim(KEY, "mail-1")
    await ledger.release(KEY, "mail-2")
    assert await ledger.claim(KEY, "mail-3") == "mail-1"
