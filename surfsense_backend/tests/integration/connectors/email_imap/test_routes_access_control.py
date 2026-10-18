"""HTTP-level access control for the Email (IMAP) connector routes."""

from __future__ import annotations

from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI

from app.connectors.email_imap.routes import router as email_imap_router
from app.db import get_async_session
from app.users import get_auth_context

from .conftest import ALICE, requires_greenmail

pytestmark = [pytest.mark.integration, requires_greenmail]


@pytest_asyncio.fixture
async def api(db_session):
    app = FastAPI()
    app.include_router(email_imap_router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth):
            acting.auth = auth
            return client

        yield as_user


async def test_raw_connection_test_does_not_persist_anything(
    api, make_owner, imap_settings, db_session
):
    """The add-form's pre-save test endpoint must never write to the DB."""
    from sqlalchemy import select

    from app.db import SearchSourceConnector

    owner = await make_owner()
    response = await api(owner.auth).post(
        "/api/v1/connectors/email-imap/test",
        params={"workspace_id": owner.workspace.id},
        json={
            "host": imap_settings["host"],
            "port": imap_settings["port"],
            "use_tls": imap_settings["use_tls"],
            "username": ALICE.username,
            "app_password": ALICE.password,
        },
    )
    assert response.status_code == 200, response.text
    assert response.json()["status"] == "ok"

    rows = (
        await db_session.execute(
            select(SearchSourceConnector).where(
                SearchSourceConnector.workspace_id == owner.workspace.id
            )
        )
    ).scalars().all()
    assert rows == []


async def test_add_account_never_returns_the_password(api, make_owner, imap_settings):
    owner = await make_owner()
    response = await api(owner.auth).post(
        f"/api/v1/workspaces/{owner.workspace.id}/connectors/email-imap/accounts",
        json={
            "label": "Alice",
            "host": imap_settings["host"],
            "port": imap_settings["port"],
            "use_tls": imap_settings["use_tls"],
            "username": ALICE.username,
            "app_password": ALICE.password,
        },
    )
    assert response.status_code == 201, response.text
    body = response.json()
    assert "app_password" not in body
    assert ALICE.password not in response.text


async def test_user_b_gets_404_for_user_a_account(
    api, make_owner, imap_settings, db_session
):
    alice_owner = await make_owner()
    bob_owner = await make_owner()

    created = await api(alice_owner.auth).post(
        f"/api/v1/workspaces/{alice_owner.workspace.id}/connectors/email-imap/accounts",
        json={
            "label": "Alice",
            "host": imap_settings["host"],
            "port": imap_settings["port"],
            "use_tls": imap_settings["use_tls"],
            "username": ALICE.username,
            "app_password": ALICE.password,
        },
    )
    account_id = created.json()["id"]

    # Bob lists accounts in his own workspace: empty.
    listing = await api(bob_owner.auth).get(
        f"/api/v1/workspaces/{bob_owner.workspace.id}/connectors/email-imap/accounts"
    )
    assert listing.status_code == 200
    assert listing.json() == []

    # Bob cannot test or delete Alice's account id even if he somehow learned it,
    # whether he names his own workspace or hers (he lacks membership in hers,
    # and even if he had it, the row is scoped to Alice's user_id).
    test_resp = await api(bob_owner.auth).post(
        f"/api/v1/workspaces/{alice_owner.workspace.id}"
        f"/connectors/email-imap/accounts/{account_id}/test"
    )
    assert test_resp.status_code in (403, 404)

    delete_resp = await api(bob_owner.auth).delete(
        f"/api/v1/workspaces/{alice_owner.workspace.id}"
        f"/connectors/email-imap/accounts/{account_id}"
    )
    assert delete_resp.status_code in (403, 404)
