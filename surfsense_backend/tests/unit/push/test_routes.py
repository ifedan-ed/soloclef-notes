"""The phone registers its token and keeps its choices; API keys are refused."""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest
from fastapi import FastAPI
from starlette.testclient import TestClient

from app.auth.context import AuthContext
from app.push.kinds import KIND_NAMES
from app.push.routes import router
from app.push.store import get_push_store
from app.users import get_auth_context
from tests.unit.push.fake_store import FakeStore

pytestmark = pytest.mark.unit

ANN = SimpleNamespace(id=uuid4(), is_superuser=False, email="ann@example.org")
BEN = SimpleNamespace(id=uuid4(), is_superuser=False, email="ben@example.org")
TOKEN = "ExponentPushToken[phone-1]"
# Each account's live sign-in (a refresh-token family), as the phone names it.
ANN_SIGN_IN = uuid4()
BEN_SIGN_IN = uuid4()


def _signed_in() -> FakeStore:
    return FakeStore(live_families={ANN.id: {ANN_SIGN_IN}, BEN.id: {BEN_SIGN_IN}})


def _phone(user=ANN, **extra) -> dict:
    sign_in = ANN_SIGN_IN if user is ANN else BEN_SIGN_IN
    return {"token": TOKEN, "platform": "ios", "session_id": str(sign_in), **extra}


def _client(user, store: FakeStore, *, pat: bool = False) -> TestClient:
    app = FastAPI()
    app.include_router(router, prefix="/api/v1")

    async def _auth() -> AuthContext:
        if pat:
            return AuthContext.pat_auth(user, SimpleNamespace(id=1))
        return AuthContext.session(user)

    async def _store() -> FakeStore:
        return store

    app.dependency_overrides[get_auth_context] = _auth
    app.dependency_overrides[get_push_store] = _store
    return TestClient(app)


def test_registering_a_phone_and_registering_it_again() -> None:
    store = _signed_in()
    client = _client(ANN, store)
    first = client.post(
        "/api/v1/push/devices",
        json=_phone(label="  iPhone  15 ", language="en-GB"),
    )
    assert first.status_code == 200
    assert first.json()["token"] == TOKEN
    assert first.json()["label"] == "iPhone 15"
    again = client.post("/api/v1/push/devices", json=_phone())
    assert again.status_code == 200
    assert again.json()["id"] == first.json()["id"]
    assert len(store.devices) == 1
    assert store.commits == 2


def test_a_phone_that_signs_into_another_account_moves_with_it() -> None:
    store = _signed_in()
    _client(ANN, store).post("/api/v1/push/devices", json=_phone(platform="android"))
    _client(BEN, store).post(
        "/api/v1/push/devices", json=_phone(BEN, platform="android")
    )
    assert [d.user_id for d in store.devices] == [BEN.id]
    assert store.devices[0].session_family_id == BEN_SIGN_IN


def test_only_expo_tokens_are_taken() -> None:
    store = _signed_in()
    res = _client(ANN, store).post("/api/v1/push/devices", json=_phone(token="abc"))
    assert res.status_code == 422
    res = _client(ANN, store).post("/api/v1/push/devices", json=_phone(platform="web"))
    assert res.status_code == 422
    assert store.devices == []


def test_forgetting_a_phone_is_idempotent_and_only_for_its_own_account() -> None:
    store = _signed_in()
    _client(ANN, store).post("/api/v1/push/devices", json=_phone())
    forget = {"token": TOKEN}
    assert (
        _client(BEN, store)
        .request("DELETE", "/api/v1/push/devices", json=forget)
        .status_code
        == 204
    )
    assert len(store.devices) == 1
    assert (
        _client(ANN, store)
        .request("DELETE", "/api/v1/push/devices", json=forget)
        .status_code
        == 204
    )
    assert store.devices == []
    assert (
        _client(ANN, store)
        .request("DELETE", "/api/v1/push/devices", json=forget)
        .status_code
        == 204
    )
    assert (
        _client(ANN, store)
        .request("DELETE", "/api/v1/push/devices", json={"token": "junk"})
        .status_code
        == 204
    )


def test_the_first_app_builds_query_string_still_forgets_a_phone() -> None:
    store = _signed_in()
    _client(ANN, store).post("/api/v1/push/devices", json=_phone())
    res = _client(ANN, store).delete(f"/api/v1/push/devices?token={TOKEN}")
    assert res.status_code == 204
    assert store.devices == []
    assert _client(ANN, store).delete("/api/v1/push/devices").status_code == 204


def test_choices_start_all_on_and_changes_merge() -> None:
    store = FakeStore()
    client = _client(ANN, store)
    initial = client.get("/api/v1/push/preferences").json()
    assert initial["choices"] == dict.fromkeys(KIND_NAMES, True)
    assert [k["kind"] for k in initial["kinds"]] == list(KIND_NAMES)
    assert all(k["label"] and k["description"] for k in initial["kinds"])

    changed = client.put(
        "/api/v1/push/preferences", json={"choices": {"answer": False}}
    )
    assert changed.status_code == 200
    assert changed.json()["choices"]["answer"] is False
    assert changed.json()["choices"]["mention"] is True

    more = client.put("/api/v1/push/preferences", json={"choices": {"mention": False}})
    assert more.json()["choices"]["answer"] is False
    assert more.json()["choices"]["mention"] is False
    assert store.choices[ANN.id] == {"answer": False, "mention": False}


def test_an_unknown_kind_is_refused() -> None:
    res = _client(ANN, FakeStore()).put(
        "/api/v1/push/preferences", json={"choices": {"smoke_signals": True}}
    )
    assert res.status_code == 422


@pytest.mark.parametrize(
    ("method", "path", "json"),
    [
        ("post", "/api/v1/push/devices", _phone()),
        ("delete", "/api/v1/push/devices", {"token": TOKEN}),
        ("get", "/api/v1/push/preferences", None),
        ("put", "/api/v1/push/preferences", {"choices": {}}),
    ],
)
def test_an_api_key_cannot_register_a_phone_or_read_choices(method, path, json) -> None:
    store = _signed_in()
    res = _client(ANN, store, pat=True).request(method, path, json=json)
    assert res.status_code == 403
    assert store.devices == []


def test_a_phone_registered_with_its_sign_in_is_bound_to_it() -> None:
    store = _signed_in()
    res = _client(ANN, store).post("/api/v1/push/devices", json=_phone())
    assert res.status_code == 200
    assert store.devices[0].session_family_id == ANN_SIGN_IN


def test_a_registration_must_name_its_sign_in() -> None:
    store = _signed_in()
    res = _client(ANN, store).post(
        "/api/v1/push/devices", json={"token": TOKEN, "platform": "ios"}
    )
    assert res.status_code == 422
    assert store.devices == []


def test_a_sign_in_that_is_not_this_accounts_live_one_registers_nothing() -> None:
    store = _signed_in()
    bens = _client(ANN, store).post(
        "/api/v1/push/devices", json=_phone(session_id=str(BEN_SIGN_IN))
    )
    assert bens.status_code == 401
    assert bens.json()["detail"]["code"] == "PUSH_SESSION_ENDED"
    ended = _client(ANN, store).post(
        "/api/v1/push/devices", json=_phone(session_id=str(uuid4()))
    )
    assert ended.status_code == 401
    assert store.devices == []
    assert store.commits == 0


def test_a_phone_the_server_signed_out_cannot_register_again_unbound() -> None:
    # Signed out from the devices list: its family ends and its row goes
    # (refresh_tokens.revoke_family). Its access token still works for up to
    # ACCESS_TOKEN_LIFETIME_SECONDS, and the app registers on every start.
    store = _signed_in()
    _client(ANN, store).post("/api/v1/push/devices", json=_phone())
    store.live_families[ANN.id] = set()
    store.devices = []
    res = _client(ANN, store).post("/api/v1/push/devices", json=_phone())
    assert res.status_code == 401
    assert store.devices == []


def test_a_session_id_that_is_not_one_is_refused() -> None:
    res = _client(ANN, _signed_in()).post(
        "/api/v1/push/devices", json=_phone(session_id="not-a-uuid")
    )
    assert res.status_code == 422


@pytest.mark.asyncio
async def test_a_phone_whose_sign_in_ended_takes_no_pushes() -> None:
    store = _signed_in()
    _client(ANN, store).post("/api/v1/push/devices", json=_phone())
    assert [d.token for d in await store.devices_of(ANN.id)] == [TOKEN]
    store.live_families[ANN.id] = set()
    assert await store.devices_of(ANN.id) == []
