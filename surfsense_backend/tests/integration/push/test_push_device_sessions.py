"""A phone bound to its sign-in takes pushes only while that sign-in is live.

The server signs phones out without their help: the devices list's Sign out
(``revoke_family``), Sign out everywhere and a password change
(``revoke_all_user_tokens``), a sign-out with the family's token
(``revoke_refresh_token``), and token reuse (``rotate_refresh_token``). Each
deletes the push tokens of the families it ends, and ``devices_of`` skips any
whose family is no longer live, so a signed-out phone never gets the
account's pushes or badge.
"""

from __future__ import annotations

from contextlib import asynccontextmanager
from datetime import UTC, datetime, timedelta

import httpx
import pytest
from fastapi import FastAPI
from sqlalchemy import select, update

from app.auth.context import AuthContext
from app.db import PushDevice, RefreshToken, get_async_session
from app.push.routes import router as push_router
from app.push.store import DbStore
from app.users import get_auth_context
from app.utils import refresh_tokens

pytestmark = pytest.mark.integration


@pytest.fixture
def same_transaction(monkeypatch, db_session):
    """refresh_tokens opens its own sessions; here they are the test's (rolled back after)."""

    @asynccontextmanager
    async def this_transaction():
        yield db_session

    monkeypatch.setattr(refresh_tokens, "async_session_maker", this_transaction)


async def _phone(db_session, user, token: str, family) -> None:
    store = DbStore(db_session)
    await store.upsert_device(
        user.id,
        token=token,
        platform="ios",
        label=None,
        language=None,
        session_family_id=family,
    )
    await db_session.flush()


async def _tokens(db_session, user) -> list[str]:
    rows = await DbStore(db_session).devices_of(user.id)
    return sorted(d.token for d in rows)


async def _rows(db_session, user) -> list[str]:
    result = await db_session.execute(
        select(PushDevice.token).where(PushDevice.user_id == user.id)
    )
    return sorted(result.scalars().all())


@pytest.mark.asyncio
async def test_a_live_sign_in_is_bound_and_takes_pushes(
    db_session, db_user, make_user, same_transaction
):
    phone = await refresh_tokens.issue_refresh_token(
        db_user.id, kind="app", device_label="iPhone"
    )
    other = await make_user()
    store = DbStore(db_session)
    assert await store.family_is_live(db_user.id, phone.family_id)
    assert not await store.family_is_live(other.id, phone.family_id)
    await _phone(db_session, db_user, "ExponentPushToken[a]", phone.family_id)
    await _phone(db_session, db_user, "ExponentPushToken[old]", None)
    assert await _tokens(db_session, db_user) == [
        "ExponentPushToken[a]",
        "ExponentPushToken[old]",
    ]


@pytest.mark.asyncio
async def test_signing_one_phone_out_from_the_devices_list_ends_its_pushes(
    db_session, db_user, same_transaction
):
    lost = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    kept = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    await _phone(db_session, db_user, "ExponentPushToken[lost]", lost.family_id)
    await _phone(db_session, db_user, "ExponentPushToken[kept]", kept.family_id)
    assert await refresh_tokens.revoke_family(db_user.id, lost.family_id)
    assert await _rows(db_session, db_user) == ["ExponentPushToken[kept]"]
    assert await _tokens(db_session, db_user) == ["ExponentPushToken[kept]"]


@pytest.mark.asyncio
async def test_sign_out_everywhere_ends_every_phones_pushes(
    db_session, db_user, make_user, same_transaction
):
    phone = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    other = await make_user()
    others = await refresh_tokens.issue_refresh_token(other.id, kind="app")
    await _phone(db_session, db_user, "ExponentPushToken[mine]", phone.family_id)
    await _phone(db_session, db_user, "ExponentPushToken[unbound]", None)
    await _phone(db_session, other, "ExponentPushToken[theirs]", others.family_id)
    await refresh_tokens.revoke_all_user_tokens(db_user.id)
    assert await _rows(db_session, db_user) == []
    assert await _tokens(db_session, other) == ["ExponentPushToken[theirs]"]


@pytest.mark.asyncio
async def test_signing_out_with_the_familys_token_ends_its_pushes(
    db_session, db_user, same_transaction
):
    phone = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    await _phone(db_session, db_user, "ExponentPushToken[a]", phone.family_id)
    assert await refresh_tokens.revoke_refresh_token(phone.token)
    assert await _rows(db_session, db_user) == []
    assert not await refresh_tokens.revoke_refresh_token("not-a-token")


@pytest.mark.asyncio
async def test_a_sign_in_that_ended_another_way_takes_no_pushes(
    db_session, db_user, same_transaction
):
    expired = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    revoked = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    await _phone(db_session, db_user, "ExponentPushToken[expired]", expired.family_id)
    await _phone(db_session, db_user, "ExponentPushToken[revoked]", revoked.family_id)
    past = datetime.now(UTC) - timedelta(minutes=1)
    await db_session.execute(
        update(RefreshToken)
        .where(RefreshToken.family_id == expired.family_id)
        .values(absolute_expiry=past)
    )
    # Revoked without going through revoke_family (as token reuse detection does).
    await db_session.execute(
        update(RefreshToken)
        .where(RefreshToken.family_id == revoked.family_id)
        .values(revoked_at=past)
    )
    await db_session.flush()
    assert await _tokens(db_session, db_user) == []
    assert not await DbStore(db_session).family_is_live(db_user.id, expired.family_id)


@pytest.mark.asyncio
async def test_token_reuse_ends_the_familys_pushes(
    db_session, db_user, same_transaction, monkeypatch
):
    phone = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    await _phone(db_session, db_user, "ExponentPushToken[a]", phone.family_id)

    # rotate_refresh_token opens a transaction of its own (session.begin()); inside the test's
    # transaction that is a savepoint.
    real_begin = db_session.begin

    def nested_begin():
        return (
            db_session.begin_nested() if db_session.in_transaction() else real_begin()
        )

    monkeypatch.setattr(db_session, "begin", nested_begin)
    rotated = await refresh_tokens.rotate_refresh_token(phone.token)
    assert rotated is not None and rotated.refresh_token
    monkeypatch.setattr(refresh_tokens.config, "REFRESH_ROTATION_GRACE_SECONDS", 0)
    # The first token again, after the grace: reuse, so the whole family ends.
    assert await refresh_tokens.rotate_refresh_token(phone.token) is None
    assert await _rows(db_session, db_user) == []


@asynccontextmanager
async def _push_api(db_session, user):
    """The real push routes over the test's session, signed in as ``user``.

    The access token is not what is checked here: a revoked sign-in's access
    token keeps working until it runs out (ACCESS_TOKEN_LIFETIME_SECONDS),
    which is the case the registration rule is for.
    """
    app = FastAPI()
    app.include_router(push_router, prefix="/api/v1")

    async def same_session():
        yield db_session

    app.dependency_overrides[get_async_session] = same_session
    app.dependency_overrides[get_auth_context] = lambda: AuthContext.session(user)
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as http:
        yield http


@pytest.mark.asyncio
async def test_a_phone_signed_out_from_the_devices_list_cannot_register_again(
    db_session, db_user, same_transaction
):
    phone = await refresh_tokens.issue_refresh_token(
        db_user.id, kind="app", device_label="iPhone"
    )
    registration = {
        "token": "ExponentPushToken[lost]",
        "platform": "ios",
        "session_id": str(phone.family_id),
    }
    async with _push_api(db_session, db_user) as http:
        bound = await http.post("/api/v1/push/devices", json=registration)
        assert bound.status_code == 200, bound.text
        assert await _tokens(db_session, db_user) == ["ExponentPushToken[lost]"]

        assert await refresh_tokens.revoke_family(db_user.id, phone.family_id)
        assert await _rows(db_session, db_user) == []

        # The app registers on every start, with an access token that still works.
        again = await http.post("/api/v1/push/devices", json=registration)
        assert again.status_code == 401
        assert again.json()["detail"]["code"] == "PUSH_SESSION_ENDED"
        unnamed = await http.post(
            "/api/v1/push/devices",
            json={"token": "ExponentPushToken[lost]", "platform": "ios"},
        )
        assert unnamed.status_code == 422

    assert await _rows(db_session, db_user) == []
    assert await _tokens(db_session, db_user) == []


@pytest.mark.asyncio
async def test_a_phone_forgets_itself_with_the_token_in_the_body(
    db_session, db_user, same_transaction
):
    phone = await refresh_tokens.issue_refresh_token(db_user.id, kind="app")
    async with _push_api(db_session, db_user) as http:
        await http.post(
            "/api/v1/push/devices",
            json={
                "token": "ExponentPushToken[a]",
                "platform": "android",
                "session_id": str(phone.family_id),
            },
        )
        gone = await http.request(
            "DELETE", "/api/v1/push/devices", json={"token": "ExponentPushToken[a]"}
        )
        assert gone.status_code == 204
    assert await _rows(db_session, db_user) == []
