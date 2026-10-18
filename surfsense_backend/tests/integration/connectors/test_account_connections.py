"""Account-level connections: default-on per workspace, owner-only management,
delete-workspace keeps the connection, cross-user isolation, agent gating.

See custom/ACCOUNT_CONNECTIONS.md for the design this exercises.
"""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI
from sqlalchemy import select

from app.auth.context import AuthContext
from app.connectors.email_imap import credentials as email_creds
from app.connectors.email_imap.service import list_accounts as email_list_accounts
from app.db import (
    Document,
    SearchSourceConnector,
    SearchSourceConnectorEnablement,
    SearchSourceConnectorType,
    User,
    Workspace,
    WorkspaceMembership,
    get_async_session,
)
from app.routes import search_source_connectors_routes
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.services.connector_enablement import (
    enabled_connectors_in_workspace,
    is_enabled_in_workspace,
)
from app.services.connector_service import ConnectorService
from app.users import get_auth_context

pytestmark = pytest.mark.integration


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def make_user(db_session):
    async def build(label: str) -> User:
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

    return build


@pytest.fixture
def make_workspace(db_session):
    async def build(owner: User, *, name: str = "Space") -> Workspace:
        workspace = Workspace(name=f"{name} {uuid.uuid4().hex[:6]}", user_id=owner.id)
        db_session.add(workspace)
        await db_session.flush()
        await create_default_roles_and_membership(db_session, workspace.id, owner.id)
        await db_session.flush()
        return workspace

    return build


@pytest.fixture
def add_member(db_session):
    async def build(workspace: Workspace, user: User) -> WorkspaceMembership:
        membership = WorkspaceMembership(
            user_id=user.id, workspace_id=workspace.id, is_owner=False
        )
        db_session.add(membership)
        await db_session.flush()
        return membership

    return build


@pytest.fixture
def make_connector(db_session):
    async def build(
        owner: User,
        connector_type: SearchSourceConnectorType = SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        *,
        name: str = "Drive",
        config: dict | None = None,
    ) -> SearchSourceConnector:
        connector = SearchSourceConnector(
            name=name,
            connector_type=connector_type,
            is_indexable=connector_type
            == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
            config=config or {},
            workspace_id=None,
            user_id=owner.id,
        )
        db_session.add(connector)
        await db_session.flush()
        return connector

    return build


@pytest_asyncio.fixture
async def connectors_api(db_session):
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

        def as_user(auth: AuthContext):
            acting.auth = auth
            return client

        yield as_user


async def _doc(db_session, *, connector_id: int, workspace_id: int) -> Document:
    doc = Document(
        title="f",
        document_type="FILE",
        content="c",
        content_hash=uuid.uuid4().hex,
        connector_id=connector_id,
        workspace_id=workspace_id,
    )
    db_session.add(doc)
    await db_session.flush()
    return doc


# ---------------------------------------------------------------------------
# Default-on, everywhere the owner belongs
# ---------------------------------------------------------------------------


async def test_connection_is_enabled_by_default_in_every_workspace_owner_belongs_to(
    db_session, make_user, make_workspace, make_connector
):
    alice = await make_user("alice")
    w1 = await make_workspace(alice, name="W1")
    w2 = await make_workspace(alice, name="W2")
    connector = await make_connector(alice)

    assert await is_enabled_in_workspace(db_session, connector, w1.id) is True
    assert await is_enabled_in_workspace(db_session, connector, w2.id) is True

    rows = (
        (
            await db_session.execute(
                select(SearchSourceConnectorEnablement).where(
                    SearchSourceConnectorEnablement.connector_id == connector.id
                )
            )
        )
        .scalars()
        .all()
    )
    assert rows == []  # no override row needed for the default


async def test_a_brand_new_workspace_already_has_existing_connections_enabled(
    db_session, make_user, make_workspace, make_connector
):
    alice = await make_user("alice")
    await make_workspace(alice, name="first")
    await make_connector(alice)

    # A workspace created *after* the connector exists.
    w_new = await make_workspace(alice, name="brand-new")

    enabled_types = {
        c.connector_type
        for c in await enabled_connectors_in_workspace(db_session, w_new.id)
    }
    assert SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR in enabled_types


# ---------------------------------------------------------------------------
# Opt-out in one workspace, leaving others on
# ---------------------------------------------------------------------------


async def test_disable_in_one_workspace_leaves_others_enabled(
    db_session, make_user, make_workspace, make_connector, connectors_api
):
    alice = await make_user("alice")
    w1 = await make_workspace(alice, name="W1")
    w2 = await make_workspace(alice, name="W2")
    connector = await make_connector(alice)
    auth = AuthContext.session(alice)

    resp = await connectors_api(auth).delete(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{w1.id}"
    )
    assert resp.status_code == 200, resp.text
    await db_session.commit()

    assert await is_enabled_in_workspace(db_session, connector, w1.id) is False
    assert await is_enabled_in_workspace(db_session, connector, w2.id) is True


async def test_disable_removes_only_that_workspaces_documents(
    db_session, make_user, make_workspace, make_connector, connectors_api
):
    alice = await make_user("alice")
    w1 = await make_workspace(alice, name="W1")
    w2 = await make_workspace(alice, name="W2")
    connector = await make_connector(alice)
    doc1 = await _doc(db_session, connector_id=connector.id, workspace_id=w1.id)
    doc2 = await _doc(db_session, connector_id=connector.id, workspace_id=w2.id)
    await db_session.commit()
    auth = AuthContext.session(alice)

    resp = await connectors_api(auth).delete(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{w1.id}"
    )
    assert resp.status_code == 200, resp.text
    assert resp.json()["documents_removed"] == 1
    await db_session.commit()

    assert await db_session.get(Document, doc1.id) is None
    assert await db_session.get(Document, doc2.id) is not None
    # The connector itself (and its credentials) are untouched.
    assert await db_session.get(SearchSourceConnector, connector.id) is not None


async def test_reenable_after_disable(
    db_session, make_user, make_workspace, make_connector, connectors_api
):
    alice = await make_user("alice")
    w1 = await make_workspace(alice, name="W1")
    connector = await make_connector(alice)
    auth = AuthContext.session(alice)

    await connectors_api(auth).delete(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{w1.id}"
    )
    await db_session.commit()
    assert await is_enabled_in_workspace(db_session, connector, w1.id) is False

    resp = await connectors_api(auth).post(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{w1.id}"
    )
    assert resp.status_code == 200, resp.text
    await db_session.commit()
    assert await is_enabled_in_workspace(db_session, connector, w1.id) is True


# ---------------------------------------------------------------------------
# Deleting a workspace keeps the connection
# ---------------------------------------------------------------------------


async def test_deleting_a_workspace_keeps_the_connection(
    db_session, make_user, make_workspace, make_connector
):
    alice = await make_user("alice")
    w1 = await make_workspace(alice, name="keep-me")
    w2 = await make_workspace(alice, name="delete-me")
    connector = await make_connector(alice)
    # An explicit override in w2, to prove the override row is what's gone.
    db_session.add(
        SearchSourceConnectorEnablement(
            connector_id=connector.id,
            workspace_id=w2.id,
            enabled_by_user_id=alice.id,
            enabled=True,
            indexing_config={"selected_folders": ["x"]},
        )
    )
    await db_session.commit()

    await db_session.delete(w2)
    await db_session.commit()

    # Connector survives, still enabled (by default) in the surviving workspace.
    survived = await db_session.get(SearchSourceConnector, connector.id)
    assert survived is not None
    assert await is_enabled_in_workspace(db_session, survived, w1.id) is True

    remaining_overrides = (
        (
            await db_session.execute(
                select(SearchSourceConnectorEnablement).where(
                    SearchSourceConnectorEnablement.connector_id == connector.id
                )
            )
        )
        .scalars()
        .all()
    )
    assert remaining_overrides == []


# ---------------------------------------------------------------------------
# Cross-user isolation
# ---------------------------------------------------------------------------


async def test_a_member_cannot_fetch_another_members_connector_by_id(
    db_session, make_user, make_workspace, add_member, make_connector, connectors_api
):
    alice = await make_user("alice")
    bob = await make_user("bob")
    shared = await make_workspace(alice, name="shared")
    await add_member(shared, bob)
    connector = await make_connector(alice)

    resp = await connectors_api(AuthContext.session(bob)).get(
        f"/api/v1/search-source-connectors/{connector.id}"
    )
    assert resp.status_code == 404


async def test_a_member_cannot_toggle_or_delete_another_members_connection(
    db_session, make_user, make_workspace, add_member, make_connector, connectors_api
):
    alice = await make_user("alice")
    bob = await make_user("bob")
    shared = await make_workspace(alice, name="shared")
    await add_member(shared, bob)
    connector = await make_connector(alice)
    bob_auth = AuthContext.session(bob)

    resp = await connectors_api(bob_auth).delete(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{shared.id}"
    )
    assert resp.status_code == 403

    resp = await connectors_api(bob_auth).post(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{shared.id}"
    )
    assert resp.status_code == 403

    resp = await connectors_api(bob_auth).delete(
        f"/api/v1/search-source-connectors/{connector.id}"
    )
    assert resp.status_code == 403

    # Still enabled — Bob's failed attempts changed nothing.
    assert await is_enabled_in_workspace(db_session, connector, shared.id) is True


async def test_a_former_member_connection_is_no_longer_enabled(
    db_session, make_user, make_workspace, add_member, make_connector
):
    alice = await make_user("alice")
    shared = await make_workspace(alice, name="shared")
    connector = await make_connector(alice)
    assert await is_enabled_in_workspace(db_session, connector, shared.id) is True

    membership = await db_session.scalar(
        select(WorkspaceMembership).where(
            WorkspaceMembership.user_id == alice.id,
            WorkspaceMembership.workspace_id == shared.id,
        )
    )
    await db_session.delete(membership)
    await db_session.commit()

    assert await is_enabled_in_workspace(db_session, connector, shared.id) is False


# ---------------------------------------------------------------------------
# Shared workspace: a member can USE an enabled connection, never its secrets
# ---------------------------------------------------------------------------


async def test_a_member_can_use_the_owners_enabled_email_through_the_agent_tool(
    db_session, make_user, make_workspace, add_member, monkeypatch
):
    from app.config import config as app_config

    monkeypatch.setattr(app_config, "SECRET_KEY", "test-secret-key")
    # fix/email-imap-hardening's SSRF guard correctly refuses an
    # unresolvable/private host (the point of its own ssrf-guard tests);
    # this test only cares about enablement/visibility, not reachability.
    monkeypatch.setattr(email_creds, "is_publicly_routable", lambda _url: True)

    alice = await make_user("alice")
    bob = await make_user("bob")
    shared = await make_workspace(alice, name="shared")
    await add_member(shared, bob)

    record = email_creds.new_account_record(
        label="Alice mail",
        host="imap.example.com",
        port=993,
        use_tls=True,
        username="alice",
        app_password="super-secret-app-password",
    )
    connector = SearchSourceConnector(
        name="Email (IMAP)",
        connector_type=SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
        is_indexable=False,
        config={"accounts": [record]},
        workspace_id=None,
        user_id=alice.id,
    )
    db_session.add(connector)
    await db_session.commit()

    # Bob, using the shared workspace's enabled connections (the chat tool path).
    accounts = await email_list_accounts(
        db_session, workspace_id=shared.id, user_id=bob.id, for_workspace_use=True
    )
    assert [a["label"] for a in accounts] == ["Alice mail"]
    assert "app_password" not in accounts[0]
    assert "super-secret-app-password" not in str(accounts[0])

    # Bob's own management view (settings page) sees none of it — he owns nothing.
    own = await email_list_accounts(
        db_session, workspace_id=shared.id, user_id=bob.id, for_workspace_use=False
    )
    assert own == []


async def test_disabling_removes_a_members_access_immediately(
    db_session, make_user, make_workspace, add_member, connectors_api, monkeypatch
):
    from app.config import config as app_config

    monkeypatch.setattr(app_config, "SECRET_KEY", "test-secret-key")
    monkeypatch.setattr(email_creds, "is_publicly_routable", lambda _url: True)

    alice = await make_user("alice")
    bob = await make_user("bob")
    shared = await make_workspace(alice, name="shared")
    await add_member(shared, bob)

    record = email_creds.new_account_record(
        label="Alice mail",
        host="imap.example.com",
        port=993,
        use_tls=True,
        username="alice",
        app_password="pw",
    )
    connector = SearchSourceConnector(
        name="Email (IMAP)",
        connector_type=SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
        is_indexable=False,
        config={"accounts": [record]},
        workspace_id=None,
        user_id=alice.id,
    )
    db_session.add(connector)
    await db_session.commit()

    before = await email_list_accounts(
        db_session, workspace_id=shared.id, user_id=bob.id, for_workspace_use=True
    )
    assert len(before) == 1

    resp = await connectors_api(AuthContext.session(alice)).delete(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{shared.id}"
    )
    assert resp.status_code == 200, resp.text
    await db_session.commit()

    after = await email_list_accounts(
        db_session, workspace_id=shared.id, user_id=bob.id, for_workspace_use=True
    )
    assert after == []


# ---------------------------------------------------------------------------
# Agent tool gating per workspace
# ---------------------------------------------------------------------------


async def test_agent_gating_reflects_enabled_connectors_per_workspace(
    db_session, make_user, make_workspace, add_member, make_connector
):
    alice = await make_user("alice")
    bob = await make_user("bob")
    w1 = await make_workspace(alice, name="w1")
    shared = await make_workspace(alice, name="shared")
    await add_member(shared, bob)
    other = await make_workspace(bob, name="bobs-own")

    await make_connector(alice)  # Google Drive, enabled everywhere Alice is
    await db_session.commit()

    svc = ConnectorService(db_session)
    assert SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR in (
        await svc.get_available_connectors(w1.id)
    )
    assert SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR in (
        await svc.get_available_connectors(shared.id)
    )
    # Bob's own workspace, where Alice is not a member: not enabled there.
    assert SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR not in (
        await svc.get_available_connectors(other.id)
    )


async def test_agent_gating_excludes_an_explicitly_disabled_connector(
    db_session, make_user, make_workspace, make_connector, connectors_api
):
    alice = await make_user("alice")
    w1 = await make_workspace(alice, name="w1")
    connector = await make_connector(alice)
    await db_session.commit()

    svc = ConnectorService(db_session)
    assert SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR in (
        await svc.get_available_connectors(w1.id)
    )

    resp = await connectors_api(AuthContext.session(alice)).delete(
        f"/api/v1/search-source-connectors/{connector.id}/workspaces/{w1.id}"
    )
    assert resp.status_code == 200, resp.text
    await db_session.commit()

    assert SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR not in (
        await svc.get_available_connectors(w1.id)
    )


# ---------------------------------------------------------------------------
# PUT /search-source-connectors/{id}: periodic/indexing fields live on the
# calling workspace's enablement row for non-MCP connectors, not the
# connector's own (vestigial) columns.
# ---------------------------------------------------------------------------


async def test_put_periodic_fields_without_workspace_id_is_refused(
    db_session, make_user, make_workspace, make_connector, connectors_api
):
    alice = await make_user("alice")
    await make_workspace(alice, name="w1")
    connector = await make_connector(alice)
    await db_session.commit()

    resp = await connectors_api(AuthContext.session(alice)).put(
        f"/api/v1/search-source-connectors/{connector.id}",
        json={"periodic_indexing_enabled": True, "indexing_frequency_minutes": 60},
    )
    assert resp.status_code == 400


async def test_put_periodic_fields_write_the_calling_workspaces_enablement(
    db_session, make_user, make_workspace, make_connector, connectors_api
):
    alice = await make_user("alice")
    w1 = await make_workspace(alice, name="w1")
    w2 = await make_workspace(alice, name="w2")
    connector = await make_connector(alice)
    await db_session.commit()

    resp = await connectors_api(AuthContext.session(alice)).put(
        f"/api/v1/search-source-connectors/{connector.id}",
        params={"workspace_id": w1.id},
        json={
            "is_indexable": True,
            "periodic_indexing_enabled": True,
            "indexing_frequency_minutes": 60,
        },
    )
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["periodic_indexing_enabled"] is True
    assert body["indexing_frequency_minutes"] == 60
    await db_session.commit()

    row = await db_session.scalar(
        select(SearchSourceConnectorEnablement).where(
            SearchSourceConnectorEnablement.connector_id == connector.id,
            SearchSourceConnectorEnablement.workspace_id == w1.id,
        )
    )
    assert row is not None
    assert row.periodic_indexing_enabled is True
    assert row.indexing_frequency_minutes == 60

    # The connector's own (vestigial, account-level) columns are untouched —
    # and w2, which never got a workspace_id, has no periodic indexing on.
    fresh = await db_session.get(SearchSourceConnector, connector.id)
    assert fresh.periodic_indexing_enabled is False
    no_row_in_w2 = await db_session.scalar(
        select(SearchSourceConnectorEnablement).where(
            SearchSourceConnectorEnablement.connector_id == connector.id,
            SearchSourceConnectorEnablement.workspace_id == w2.id,
        )
    )
    assert no_row_in_w2 is None


async def test_put_periodic_fields_for_a_workspace_the_owner_is_not_in_is_refused(
    db_session, make_user, make_workspace, make_connector, connectors_api
):
    # Alice names Bob's workspace by its id: before, that made an enablement
    # row there and the schedule checker indexed Alice's files into it.
    alice = await make_user("alice")
    bob = await make_user("bob")
    await make_workspace(alice, name="alice's")
    bobs = await make_workspace(bob, name="bob's")
    connector = await make_connector(alice)
    await db_session.commit()

    resp = await connectors_api(AuthContext.session(alice)).put(
        f"/api/v1/search-source-connectors/{connector.id}",
        params={"workspace_id": bobs.id},
        json={
            "is_indexable": True,
            "periodic_indexing_enabled": True,
            "indexing_frequency_minutes": 60,
        },
    )
    assert resp.status_code == 404, resp.text
    await db_session.commit()
    row = await db_session.scalar(
        select(SearchSourceConnectorEnablement).where(
            SearchSourceConnectorEnablement.connector_id == connector.id,
            SearchSourceConnectorEnablement.workspace_id == bobs.id,
        )
    )
    assert row is None
