"""Pending logins and attempt limits against a real Redis (TEST_REDIS_URL)."""

from __future__ import annotations

import os
import uuid

import pytest
import redis.asyncio as aioredis

from app.connectors.nextcloud.login_flow.attempt_limits import POLL_LIMIT, AttemptLimits
from app.connectors.nextcloud.login_flow.pending_logins import (
    LIFETIME_SECONDS,
    PendingLogin,
    PendingLogins,
    new_flow_id,
)

pytestmark = [
    pytest.mark.integration,
    pytest.mark.skipif(
        not os.environ.get("TEST_REDIS_URL"), reason="TEST_REDIS_URL is not set"
    ),
]


@pytest.fixture
async def redis_client():
    client = aioredis.from_url(os.environ["TEST_REDIS_URL"], decode_responses=True)
    yield client
    await client.aclose()


async def test_a_pending_login_expires_with_nextclouds(redis_client):
    pending = PendingLogins(redis_client, secret_key="k")
    flow_id = new_flow_id()
    user = uuid.uuid4()
    await pending.add(
        flow_id,
        PendingLogin(
            user_id=str(user),
            workspace_id=1,
            poll_endpoint="https://cloud.example.com/login/v2/poll",
            poll_token="poll-secret",
            login_url="https://cloud.example.com/login/v2/flow/x",
        ),
    )
    key = f"soloclef:nextcloud:login-flow:{flow_id}"
    assert "poll-secret" not in await redis_client.get(key)
    assert 0 < await redis_client.ttl(key) <= LIFETIME_SECONDS
    assert (await pending.get(flow_id)).belongs_to(user, 1)
    await pending.remove(flow_id)
    assert await pending.get(flow_id) is None


async def test_the_poll_limit_resets_with_its_window(redis_client):
    limits = AttemptLimits(redis_client)
    user = uuid.uuid4()
    for _ in range(POLL_LIMIT[0]):
        assert await limits.may_poll(user)
    assert not await limits.may_poll(user)
    key = f"soloclef:nextcloud:login-poll:{user}"
    assert 0 < await redis_client.ttl(key) <= POLL_LIMIT[1]
    await redis_client.delete(key)
