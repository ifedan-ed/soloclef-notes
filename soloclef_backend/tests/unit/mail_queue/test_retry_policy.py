"""Retries back off, and stop."""

from __future__ import annotations

import pytest

from app.mail_queue.retry_policy import MAX_ATTEMPTS, retry_delay

pytestmark = pytest.mark.unit


def test_delays_grow():
    delays = [retry_delay(n) for n in range(1, MAX_ATTEMPTS)]
    assert all(d is not None for d in delays)
    assert delays == sorted(delays)


def test_no_retry_after_the_last_try():
    assert retry_delay(MAX_ATTEMPTS) is None
