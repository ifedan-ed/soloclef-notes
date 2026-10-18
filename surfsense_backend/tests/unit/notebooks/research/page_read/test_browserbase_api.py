"""Browserbase's REST API, against a fake at the HTTP boundary."""

from __future__ import annotations

import json

import pytest

from app.notebooks.research.page_read import browserbase_api
from app.notebooks.research.page_read.browserbase_api import (
    BrowserbaseApi,
    BrowserbaseError,
    browserbase_session,
)
from tests.e2e.fakes.browserbase import E2E_KEY, FakeBrowserbase

pytestmark = pytest.mark.unit


@pytest.fixture(autouse=True)
def _forget_projects(monkeypatch):
    monkeypatch.setattr(browserbase_api, "_project_ids", {})


def _api(fake: FakeBrowserbase, key: str = E2E_KEY) -> BrowserbaseApi:
    return BrowserbaseApi(key, base_url="https://bb.test", transport=fake.transport())


async def test_a_session_is_started_with_the_key_and_a_hard_timeout_then_released():
    fake = FakeBrowserbase()
    async with browserbase_session(_api(fake)) as session:
        assert session.connect_url.startswith("wss://")
        assert fake.open_sessions == [session.id]
    assert fake.open_sessions == []
    create = fake.requests[0]
    assert create.headers["x-bb-api-key"] == E2E_KEY
    body = json.loads(create.content)
    assert body["timeout"] == browserbase_api.SESSION_TIMEOUT_S
    assert body["keepAlive"] is False
    release = json.loads(fake.requests[-1].content)
    assert release["status"] == "REQUEST_RELEASE"


async def test_the_session_is_released_even_when_reading_fails():
    fake = FakeBrowserbase()
    with pytest.raises(RuntimeError):
        async with browserbase_session(_api(fake)):
            raise RuntimeError("page crashed")
    assert len(fake.created) == 1
    assert fake.open_sessions == []


async def test_a_key_that_needs_a_project_gets_its_first_project():
    fake = FakeBrowserbase(require_project=True)
    async with browserbase_session(_api(fake)) as session:
        assert session.project_id == "proj_e2e"
    assert [r.url.path for r in fake.requests] == [
        "/v1/sessions",
        "/v1/projects",
        "/v1/sessions",
        f"/v1/sessions/{session.id}",
    ]
    assert json.loads(fake.requests[-1].content)["projectId"] == "proj_e2e"


async def test_a_rejected_key_is_reported_and_nothing_is_left_open():
    fake = FakeBrowserbase(reject_key=True)
    with pytest.raises(BrowserbaseError) as caught:
        async with browserbase_session(_api(fake)):
            pass
    assert caught.value.code == "browserbase_key_rejected"
    assert fake.created == []


async def test_a_failed_release_is_logged_not_raised():
    fake = FakeBrowserbase()
    api = _api(fake)
    session = await api.create_session()
    fake.key = "rotated"  # the release now answers 401
    await api.release_session(session)
