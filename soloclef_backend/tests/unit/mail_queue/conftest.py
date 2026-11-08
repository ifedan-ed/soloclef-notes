"""Fixtures for the mail queue's unit tests."""

from __future__ import annotations

import pytest

from tests.unit.mail_queue.fake_redis import FakeRedis


@pytest.fixture
def fake_redis() -> FakeRedis:
    return FakeRedis()
