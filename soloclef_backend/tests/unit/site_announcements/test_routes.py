"""Only site administrators may list, send or withdraw announcements."""

from __future__ import annotations

from types import SimpleNamespace
from uuid import UUID, uuid4

import pytest
from fastapi import FastAPI
from starlette.testclient import TestClient

from app.auth.context import AuthContext
from app.site_announcements.routes import router
from app.site_announcements.store import get_announcement_store
from app.users import require_session_context
from tests.unit.site_announcements.fake_store import FakeStore

pytestmark = pytest.mark.unit

ADMIN = SimpleNamespace(id=uuid4(), is_superuser=True, email="admin@example.org")
MEMBER = SimpleNamespace(id=uuid4(), is_superuser=False, email="member@example.org")


def _client(user, store: FakeStore) -> TestClient:
    app = FastAPI()
    app.include_router(router, prefix="/api/v1")

    async def _session() -> AuthContext:
        return AuthContext.session(user)

    async def _store() -> FakeStore:
        return store

    app.dependency_overrides[require_session_context] = _session
    app.dependency_overrides[get_announcement_store] = _store
    return TestClient(app)


BODY = {"title": "Hello", "body": "Welcome to **Soloclef**."}


@pytest.mark.parametrize(
    ("method", "path", "json"),
    [
        ("get", "/api/v1/admin/announcements", None),
        ("post", "/api/v1/admin/announcements", BODY),
        ("post", "/api/v1/admin/announcements/1/withdraw", None),
    ],
)
def test_members_are_refused(method: str, path: str, json) -> None:
    store = FakeStore(users=[MEMBER.id])
    res = _client(MEMBER, store).request(method, path, json=json)
    assert res.status_code == 403
    assert store.inbox == []


def test_an_email_listed_admin_may_send(monkeypatch) -> None:
    from app.billing import ledger

    monkeypatch.setattr(ledger, "admin_emails", lambda: {"listed@example.org"})
    listed = SimpleNamespace(
        id=uuid4(), is_superuser=False, is_verified=True, email="listed@example.org"
    )
    store = FakeStore(users=[listed.id, ADMIN.id])
    res = _client(listed, store).post("/api/v1/admin/announcements", json=BODY)
    assert res.status_code == 201


def test_a_listed_address_on_an_unverified_account_may_not_send(monkeypatch) -> None:
    # Someone who signed up with the administrator's address (never verified).
    from app.billing import ledger

    monkeypatch.setattr(ledger, "admin_emails", lambda: {"listed@example.org"})
    planted = SimpleNamespace(
        id=uuid4(), is_superuser=False, is_verified=False, email="listed@example.org"
    )
    store = FakeStore(users=[planted.id])
    res = _client(planted, store).post("/api/v1/admin/announcements", json=BODY)
    assert res.status_code == 403
    assert store.inbox == []


def test_admin_sends_lists_and_withdraws() -> None:
    other = uuid4()
    store = FakeStore(users=[ADMIN.id, other])
    client = _client(ADMIN, store)

    sent = client.post("/api/v1/admin/announcements", json=BODY)
    assert sent.status_code == 201
    assert sent.json()["recipient_count"] == 2
    assert sent.json()["status"] == "live"
    assert {row.user_id for row in store.inbox} == {ADMIN.id, other}

    listed = client.get("/api/v1/admin/announcements").json()
    assert [a["title"] for a in listed["announcements"]] == ["Hello"]

    announcement_id = sent.json()["id"]
    withdrawn = client.post(f"/api/v1/admin/announcements/{announcement_id}/withdraw")
    assert withdrawn.status_code == 200
    assert withdrawn.json()["status"] == "withdrawn"
    assert store.inbox == []


def test_bad_drafts_and_unknown_targets() -> None:
    store = FakeStore(users=[ADMIN.id])
    client = _client(ADMIN, store)
    bad = client.post(
        "/api/v1/admin/announcements", json={**BODY, "link": "javascript:alert(1)"}
    )
    assert bad.status_code == 422
    missing_ws = client.post(
        "/api/v1/admin/announcements",
        json={**BODY, "audience": "workspace", "workspace_id": 9},
    )
    assert missing_ws.status_code == 404
    assert client.post("/api/v1/admin/announcements/5/withdraw").status_code == 404
    assert store.inbox == []


def test_sender_is_recorded() -> None:
    store = FakeStore(users=[ADMIN.id])
    _client(ADMIN, store).post("/api/v1/admin/announcements", json=BODY)
    assert store.announcements[1].created_by_id == ADMIN.id
    assert isinstance(store.announcements[1].created_by_id, UUID)
