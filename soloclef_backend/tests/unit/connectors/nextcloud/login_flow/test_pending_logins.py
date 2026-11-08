"""Pending logins: bound to who started them, poll token encrypted at rest, and rate limits."""

import uuid

import pytest

from app.config import config
from app.connectors.nextcloud.login_flow.attempt_limits import AttemptLimits
from app.connectors.nextcloud.login_flow.pending_logins import (
    LIFETIME_SECONDS,
    PendingLogin,
    PendingLogins,
    new_flow_id,
)

pytestmark = pytest.mark.unit


class DictStore:
    def __init__(self):
        self.values: dict[str, str] = {}
        self.ttls: dict[str, int] = {}
        self.counts: dict[str, int] = {}

    async def set(self, key, value, ex):
        self.values[key] = value
        self.ttls[key] = ex

    async def get(self, key):
        return self.values.get(key)

    async def delete(self, key):
        self.values.pop(key, None)

    async def incr(self, key):
        self.counts[key] = self.counts.get(key, 0) + 1
        return self.counts[key]

    async def expire(self, key, seconds, nx=False):
        self.ttls[key] = seconds


def _login(user_id, workspace_id=7):
    return PendingLogin(
        user_id=str(user_id),
        workspace_id=workspace_id,
        poll_endpoint="https://cloud.example.com/login/v2/poll",
        poll_token="very-secret-poll-token",
        login_url="https://cloud.example.com/login/v2/flow/x",
    )


async def test_a_pending_login_is_kept_encrypted_for_twenty_minutes():
    store = DictStore()
    pending = PendingLogins(store, secret_key="k")
    user = uuid.uuid4()
    flow_id = new_flow_id()
    await pending.add(flow_id, _login(user))

    assert "very-secret-poll-token" not in "".join(store.values.values())
    assert set(store.ttls.values()) == {LIFETIME_SECONDS}
    back = await pending.get(flow_id)
    assert back.poll_token == "very-secret-poll-token"
    assert back.belongs_to(user, 7)
    assert not back.belongs_to(uuid.uuid4(), 7)
    assert not back.belongs_to(user, 8)

    await pending.remove(flow_id)
    assert await pending.get(flow_id) is None


def test_flow_ids_are_unguessable():
    ids = {new_flow_id() for _ in range(100)}
    assert len(ids) == 100
    assert all(len(i) >= 32 for i in ids)


async def test_starting_logins_is_limited_per_person():
    limits = AttemptLimits(DictStore())
    me, colleague = uuid.uuid4(), uuid.uuid4()
    allowed = [
        await limits.may_start(me)
        for _ in range(config.NEXTCLOUD_LOGIN_START_LIMIT + 1)
    ]
    assert allowed[:-1] == [True] * config.NEXTCLOUD_LOGIN_START_LIMIT
    assert allowed[-1] is False
    assert await limits.may_start(colleague) is True
