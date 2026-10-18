"""The generic connector routes and Email (IMAP): mailboxes can't be copied.

A connection enabled in a shared workspace is listed to every member. Its
mailboxes must come back without their encrypted passwords or Microsoft
tokens, and the generic create/update must never write ``config.accounts``:
otherwise a member could copy someone's encrypted grant into their own
connection, where the server would decrypt it and sign in as them (and a
self-renewing Microsoft grant would outlive the owner's Remove).
"""

from __future__ import annotations

import copy
import uuid
from datetime import UTC, datetime, timedelta
from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI
from sqlalchemy import select

from app.auth.context import AuthContext
from app.config import config as app_config
from app.connectors.email_imap import credentials as creds, service
from app.connectors.email_imap.microsoft.account_record import (
    new_mailbox_record,
    stored_refresh_token,
)
from app.connectors.email_imap.microsoft.mailbox_identity import Mailbox
from app.connectors.email_imap.microsoft.token_endpoint import TokenGrant
from app.db import (
    SearchSourceConnector,
    SearchSourceConnectorType,
    User,
    Workspace,
    WorkspaceMembership,
    WorkspaceRole,
    get_async_session,
)
from app.routes import search_source_connectors_routes
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.users import get_auth_context

pytestmark = pytest.mark.integration

SECRET = "test-secret-key-that-is-32-bytes-or-more"
EMAIL_IMAP = SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR


@pytest_asyncio.fixture
async def generic_api(db_session):
    app = FastAPI()
    app.include_router(search_source_connectors_routes.router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(user: User):
            acting.auth = AuthContext.session(user)
            return client

        yield as_user


async def _user(db_session, label: str) -> User:
    user = User(
        id=uuid.uuid4(),
        email=f"{label}-{uuid.uuid4().hex[:8]}@surfsense.test",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    return user


@pytest_asyncio.fixture
async def shared(db_session, monkeypatch):
    """the operator's mailboxes (a password one and a Microsoft one), his
    connection on in workspace W, where Bob is only a Viewer."""
    monkeypatch.setattr(app_config, "SECRET_KEY", SECRET)
    operator = await _user(db_session, "operator")
    bob = await _user(db_session, "bob")
    workspace = Workspace(name=f"W {uuid.uuid4().hex[:6]}", user_id=operator.id)
    db_session.add(workspace)
    await db_session.flush()
    await create_default_roles_and_membership(db_session, workspace.id, operator.id)
    viewer = (
        await db_session.execute(
            select(WorkspaceRole).where(
                WorkspaceRole.workspace_id == workspace.id,
                WorkspaceRole.name == "Viewer",
            )
        )
    ).scalar_one()
    db_session.add(
        WorkspaceMembership(
            user_id=bob.id, workspace_id=workspace.id, role_id=viewer.id, is_owner=False
        )
    )
    password_record = creds.new_account_record(
        label="the operator work",
        host="mail.example.com",
        port=993,
        use_tls=True,
        username="operator",
        app_password="operators-app-password",
    )
    microsoft_record = new_mailbox_record(
        Mailbox(address="operator@contoso.example", subject="tid-1:oid-1"),
        TokenGrant(
            access_token="operators-access-token",
            refresh_token="operators-refresh-token",
            expires_at=datetime.now(UTC) + timedelta(hours=1),
            scopes=frozenset(),
        ),
        secret_key=SECRET,
    )
    connector = SearchSourceConnector(
        name=service.CONNECTOR_NAME,
        connector_type=EMAIL_IMAP,
        is_indexable=False,
        config={"accounts": [password_record, microsoft_record]},
        workspace_id=None,
        user_id=operator.id,
    )
    db_session.add(connector)
    await db_session.flush()
    return SimpleNamespace(
        operator=operator,
        bob=bob,
        workspace=workspace,
        connector=connector,
        password_record=password_record,
        microsoft_record=microsoft_record,
    )


def _ciphertexts(*records: dict) -> list[str]:
    return [
        r[field]
        for r in records
        for field in ("app_password", "access_token", "refresh_token")
        if r.get(field)
    ]


async def _bobs_own_connection(db_session, bob: User) -> SearchSourceConnector:
    row = SearchSourceConnector(
        name=service.CONNECTOR_NAME,
        connector_type=EMAIL_IMAP,
        is_indexable=False,
        config={"accounts": []},
        workspace_id=None,
        user_id=bob.id,
    )
    db_session.add(row)
    await db_session.flush()
    return row


async def test_a_member_sees_the_mailboxes_but_none_of_their_secrets(
    generic_api, shared
):
    response = await generic_api(shared.bob).get(
        "/api/v1/search-source-connectors",
        params={"workspace_id": shared.workspace.id},
    )
    assert response.status_code == 200, response.text
    [listed] = [c for c in response.json() if c["id"] == shared.connector.id]
    assert [a["auth"] for a in listed["config"]["accounts"]] == [
        "password",
        "microsoft",
    ]
    for secret in _ciphertexts(shared.password_record, shared.microsoft_record):
        assert secret not in response.text
    assert "_encrypted" not in response.text


async def test_the_owner_reading_their_own_connection_gets_no_secrets_either(
    generic_api, shared
):
    response = await generic_api(shared.operator).get(
        f"/api/v1/search-source-connectors/{shared.connector.id}"
    )
    assert response.status_code == 200, response.text
    assert len(response.json()["config"]["accounts"]) == 2
    for secret in _ciphertexts(shared.password_record, shared.microsoft_record):
        assert secret not in response.text


async def test_a_copied_mailbox_cant_be_written_into_another_connection(
    generic_api, db_session, shared
):
    bobs = await _bobs_own_connection(db_session, shared.bob)
    copied = copy.deepcopy(shared.microsoft_record)
    # The password-account rewrite: the operator's token as the "password", sent
    # to a server Bob runs.
    repurposed = {
        **copy.deepcopy(shared.password_record),
        "id": str(uuid.uuid4()),
        "host": "imap.bob.example",
        "app_password": shared.microsoft_record["access_token"],
    }

    response = await generic_api(shared.bob).put(
        f"/api/v1/search-source-connectors/{bobs.id}",
        json={"config": {"accounts": [copied, repurposed]}},
    )

    assert response.status_code == 200, response.text
    await db_session.refresh(bobs)
    assert bobs.config["accounts"] == []
    for secret in _ciphertexts(shared.password_record, shared.microsoft_record):
        assert secret not in str(bobs.config)
    # So nothing of the operator's can be signed in to from Bob's connection.
    with pytest.raises(service.EmailAccountNotFoundError):
        await service.test_connection(
            db_session,
            workspace_id=shared.workspace.id,
            user_id=shared.bob.id,
            account_id=copied["id"],
        )


async def test_a_copied_mailbox_cant_start_a_new_connection(
    generic_api, db_session, shared
):
    own_workspace = Workspace(name=f"Bob {uuid.uuid4().hex[:6]}", user_id=shared.bob.id)
    db_session.add(own_workspace)
    await db_session.flush()
    await create_default_roles_and_membership(
        db_session, own_workspace.id, shared.bob.id
    )

    response = await generic_api(shared.bob).post(
        "/api/v1/search-source-connectors",
        params={"workspace_id": own_workspace.id},
        json={
            "name": "Mail",
            "connector_type": EMAIL_IMAP.value,
            "is_indexable": False,
            "config": {"accounts": [copy.deepcopy(shared.microsoft_record)]},
        },
    )

    assert response.status_code == 400, response.text
    rows = (
        (
            await db_session.execute(
                select(SearchSourceConnector).where(
                    SearchSourceConnector.user_id == shared.bob.id,
                    SearchSourceConnector.connector_type == EMAIL_IMAP,
                )
            )
        )
        .scalars()
        .all()
    )
    assert rows == []


async def test_another_connection_cant_be_turned_into_email(
    generic_api, db_session, shared
):
    planted = SearchSourceConnector(
        name="Drive",
        connector_type=SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        is_indexable=True,
        config={"accounts": [copy.deepcopy(shared.microsoft_record)]},
        workspace_id=None,
        user_id=shared.bob.id,
    )
    db_session.add(planted)
    await db_session.flush()

    response = await generic_api(shared.bob).put(
        f"/api/v1/search-source-connectors/{planted.id}",
        json={"connector_type": EMAIL_IMAP.value},
    )

    assert response.status_code == 400, response.text
    await db_session.refresh(planted)
    assert planted.connector_type == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR


async def test_the_owner_renaming_with_the_listed_config_keeps_the_mailboxes(
    generic_api, db_session, shared
):
    listed = await generic_api(shared.operator).get(
        f"/api/v1/search-source-connectors/{shared.connector.id}"
    )
    response = await generic_api(shared.operator).put(
        f"/api/v1/search-source-connectors/{shared.connector.id}",
        json={"name": "My mail", "config": listed.json()["config"]},
    )

    assert response.status_code == 200, response.text
    await db_session.refresh(shared.connector)
    assert shared.connector.name == "My mail"
    stored = shared.connector.config["accounts"]
    assert [a["id"] for a in stored] == [
        shared.password_record["id"],
        shared.microsoft_record["id"],
    ]
    assert creds.decrypt_password(stored[0]) == "operators-app-password"
    assert stored_refresh_token(stored[1], secret_key=SECRET) == "operators-refresh-token"
