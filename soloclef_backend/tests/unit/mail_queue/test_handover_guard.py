"""Which messages have been handed to the mail server, and whose claim is stale."""

from __future__ import annotations

import pytest

from app.mail_queue.dedupe import keys
from app.mail_queue.dedupe.handover_guard import HandoverGuard

pytestmark = pytest.mark.unit

MAIL = "aaaabbbbccccddddeeeeffff00001111"


async def test_the_first_sender_to_claim_a_message_may_send_it(fake_redis):
    assert await HandoverGuard(fake_redis).claim(MAIL, 0) is None


async def test_a_second_sender_is_refused_while_the_first_holds_it(fake_redis):
    guard = HandoverGuard(fake_redis)
    await guard.claim(MAIL, 0)
    assert await guard.claim(MAIL, 0) == keys.CLAIMED


async def test_a_sent_message_is_refused_and_says_so(fake_redis):
    guard = HandoverGuard(fake_redis)
    await guard.claim(MAIL, 0)
    await guard.mark_sent(MAIL, 0)
    assert await guard.claim(MAIL, 0) == keys.SENT


async def test_the_next_attempt_of_the_same_mail_is_free_to_send(fake_redis):
    guard = HandoverGuard(fake_redis)
    await guard.claim(MAIL, 0)
    await guard.mark_sent(MAIL, 0)
    assert await guard.claim(MAIL, 1) is None


async def test_a_stale_claim_is_released_so_the_message_goes_out_again(fake_redis):
    guard = HandoverGuard(fake_redis)
    await guard.claim(MAIL, 0)
    await guard.release(MAIL, 0)
    assert await guard.claim(MAIL, 0) is None


async def test_releasing_never_forgets_that_a_message_was_sent(fake_redis):
    guard = HandoverGuard(fake_redis)
    await guard.claim(MAIL, 0)
    await guard.mark_sent(MAIL, 0)
    await guard.release(MAIL, 0)
    assert await guard.claim(MAIL, 0) == keys.SENT


async def test_after_the_window_a_recovered_message_is_sent_again(fake_redis):
    guard = HandoverGuard(fake_redis)
    await guard.claim(MAIL, 0)
    await guard.mark_sent(MAIL, 0)
    fake_redis.now += keys.WINDOW_SECONDS + 1
    assert await guard.claim(MAIL, 0) is None
