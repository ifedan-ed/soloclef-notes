"""Connecting Nextcloud in one click: start, poll, reconnect, disconnect.

Nextcloud (Login Flow, OCS, WebDAV) is faked at the HTTP boundary and the
pending logins live in a dict; the database, permissions and connector rows
are real.
"""

from __future__ import annotations

from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI
from sqlalchemy import select

from app.auth.context import AuthContext
from app.config import config
from app.connectors.nextcloud.connector_credentials import (
    account_from_config,
    stored_config,
)
from app.connectors.nextcloud.login_flow import routes as connect_routes, wiring
from app.connectors.nextcloud.login_flow.attempt_limits import (
    POLL_LIMIT,
    AttemptLimits,
)
from app.connectors.nextcloud.login_flow.pending_logins import PendingLogins
from app.db import (
    Document,
    DocumentType,
    SearchSourceConnector,
    SearchSourceConnectorType,
    get_async_session,
)
from app.users import get_auth_context
from tests.unit.connectors.nextcloud.fake_login_server import FakeLoginServer
from tests.unit.connectors.nextcloud.login_flow.test_pending_logins import DictStore

pytestmark = pytest.mark.integration


@pytest.fixture
def cloud() -> FakeLoginServer:
    return FakeLoginServer()


@pytest_asyncio.fixture
async def connect_api(db_session, cloud):
    app = FastAPI()
    app.include_router(connect_routes.router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)
    store = DictStore()

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[wiring.get_pending_logins] = lambda: PendingLogins(store)
    app.dependency_overrides[wiring.get_attempt_limits] = lambda: AttemptLimits(store)
    app.dependency_overrides[wiring.get_login_api_factory] = lambda: (
        lambda base_url: cloud.api(base_url)
    )
    app.dependency_overrides[wiring.get_webdav_client_factory] = lambda: cloud.webdav
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth: AuthContext):
            acting.auth = auth
            return client

        as_user.store = store
        yield as_user


def _ws(workspace_id: int, tail: str) -> str:
    return f"/api/v1/workspaces/{workspace_id}/nextcloud/{tail}"


async def _rows(db_session, owner):
    return (
        await db_session.scalars(
            select(SearchSourceConnector).where(
                SearchSourceConnector.user_id == owner.user.id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR,
            )
        )
    ).all()


async def _start(client, owner) -> dict:
    response = await client.post(_ws(owner.workspace.id, "login"))
    assert response.status_code == 200, response.text
    return response.json()


async def test_the_connection_names_the_configured_server(connect_api, make_owner):
    owner = await make_owner()
    response = await connect_api(owner.auth).get(_ws(owner.workspace.id, "connection"))
    assert response.status_code == 200
    assert response.json() == {
        "configured": True,
        "server": "cloud.example.com",
        "connector_id": None,
        "account": None,
        "needs_reconnect": False,
    }


async def test_connecting_stores_the_granted_login_encrypted(
    connect_api, db_session, cloud, make_owner
):
    owner = await make_owner()
    client = connect_api(owner.auth)

    started = await _start(client, owner)
    assert started["login_url"].startswith("https://cloud.example.com/login/v2/flow/")
    assert set(started) == {"flow_id", "login_url", "expires_in"}
    poll_token = next(iter(cloud.flows))
    assert poll_token not in str(started)

    poll = _ws(owner.workspace.id, f"login/{started['flow_id']}/poll")
    waiting = await client.post(poll)
    assert waiting.json() == {"status": "pending", "connection": None}

    password = cloud.grant()
    done = await client.post(poll)
    assert done.status_code == 200, done.text
    body = done.json()
    assert body["status"] == "connected"
    assert body["connection"]["account"] == {
        "user_id": "alice",
        "display_name": "Alice Liddell",
    }
    assert password not in done.text

    [row] = await _rows(db_session, owner)
    # Account-level connector now (custom/ACCOUNT_CONNECTIONS.md): no single
    # workspace, enabled by default in every workspace the owner belongs to.
    assert row.workspace_id is None
    assert row.is_indexable is False
    assert password not in str(row.config)
    account = account_from_config(row.config)
    assert (account.username, account.auth_name, account.app_password) == (
        "alice",
        "alice@example.com",
        password,
    )
    # The login was proven against the person's files before it was saved.
    assert any(r.method == "PROPFIND" for r in cloud.requests)
    # The flow is spent.
    again = await client.post(poll)
    assert again.status_code == 404


async def test_someone_elses_flow_id_cannot_finish_their_connection(
    connect_api, db_session, cloud, make_owner, make_user, add_member
):
    owner = await make_owner()
    colleague = SimpleNamespace(user=await make_user())
    await add_member(owner.workspace, colleague.user)
    colleague.auth = AuthContext.session(colleague.user)

    started = await _start(connect_api(owner.auth), owner)
    cloud.grant()
    poll = _ws(owner.workspace.id, f"login/{started['flow_id']}/poll")

    stolen = await connect_api(colleague.auth).post(poll)
    assert stolen.status_code == 404
    assert await _rows(db_session, colleague) == []
    cancelled = await connect_api(colleague.auth).delete(
        _ws(owner.workspace.id, f"login/{started['flow_id']}")
    )
    assert cancelled.status_code == 204

    # Still intact for the person who started it.
    mine = await connect_api(owner.auth).post(poll)
    assert mine.json()["status"] == "connected"


async def test_a_flow_only_finishes_in_the_workspace_it_was_started_in(
    connect_api, make_owner, cloud
):
    owner = await make_owner()
    started = await _start(connect_api(owner.auth), owner)
    cloud.grant()
    stranger_space = await make_owner()
    response = await connect_api(owner.auth).post(
        _ws(stranger_space.workspace.id, f"login/{started['flow_id']}/poll")
    )
    assert response.status_code in (403, 404)


async def test_a_cancelled_login_is_forgotten(connect_api, make_owner, cloud):
    owner = await make_owner()
    client = connect_api(owner.auth)
    started = await _start(client, owner)
    response = await client.delete(
        _ws(owner.workspace.id, f"login/{started['flow_id']}")
    )
    assert response.status_code == 204
    assert connect_api.store.values == {}
    cloud.grant()
    poll = await client.post(
        _ws(owner.workspace.id, f"login/{started['flow_id']}/poll")
    )
    assert poll.status_code == 404


async def test_reconnecting_keeps_the_row_and_retires_the_old_password(
    connect_api, db_session, cloud, make_owner
):
    owner = await make_owner()
    client = connect_api(owner.auth)
    first = await _start(client, owner)
    old_password = cloud.grant()
    await client.post(_ws(owner.workspace.id, f"login/{first['flow_id']}/poll"))
    [row] = await _rows(db_session, owner)

    second = await _start(client, owner)
    new_password = cloud.grant()
    done = await client.post(_ws(owner.workspace.id, f"login/{second['flow_id']}/poll"))
    assert done.json()["connection"]["connector_id"] == row.id

    rows = await _rows(db_session, owner)
    assert [r.id for r in rows] == [row.id]
    assert account_from_config(rows[0].config).app_password == new_password
    assert cloud.revoked == [old_password]


async def test_the_owners_pasted_login_keeps_working_and_is_upgraded_in_place(
    connect_api, db_session, cloud, make_owner, make_connector
):
    owner = await make_owner()
    legacy = await make_connector(owner)  # saved from a pasted app password
    legacy.config = {
        k: v
        for k, v in legacy.config.items()
        if k not in ("auth_method", "login_name", "display_name")
    }
    await db_session.flush()
    client = connect_api(owner.auth)

    status = (await client.get(_ws(owner.workspace.id, "connection"))).json()
    assert status["connector_id"] == legacy.id
    assert status["account"] == {"user_id": "alice", "display_name": "alice"}
    assert status["needs_reconnect"] is False

    started = await _start(client, owner)
    cloud.grant()
    await client.post(_ws(owner.workspace.id, f"login/{started['flow_id']}/poll"))
    [row] = await _rows(db_session, owner)
    assert row.id == legacy.id
    assert row.config["auth_method"] == "login_flow"


async def test_a_login_for_another_server_is_marked_for_reconnect(
    connect_api, db_session, make_owner, make_connector
):
    owner = await make_owner()
    connector = await make_connector(owner)
    connector.config = stored_config("https://old-cloud.example.org", "alice", "pw")
    await db_session.flush()
    status = (
        await connect_api(owner.auth).get(_ws(owner.workspace.id, "connection"))
    ).json()
    assert status["needs_reconnect"] is True


async def test_disconnecting_revokes_the_password_and_keeps_imported_files(
    connect_api, db_session, cloud, make_owner
):
    owner = await make_owner()
    client = connect_api(owner.auth)
    started = await _start(client, owner)
    password = cloud.grant()
    done = await client.post(
        _ws(owner.workspace.id, f"login/{started['flow_id']}/poll")
    )
    connector_id = done.json()["connection"]["connector_id"]
    document = Document(
        title="fluids.md",
        document_type=DocumentType.FILE,
        content="# Fluids",
        content_hash="nc-disconnect-test",
        workspace_id=owner.workspace.id,
        created_by_id=owner.user.id,
        document_metadata={"nextcloud": {"connector_id": connector_id}},
    )
    db_session.add(document)
    await db_session.flush()

    response = await client.delete(
        f"/api/v1/connectors/{connector_id}/nextcloud/connection"
    )
    assert response.status_code == 200
    assert response.json() == {"revoked": True}
    assert cloud.revoked == [password]
    assert await _rows(db_session, owner) == []
    assert await db_session.get(Document, document.id) is not None


async def test_an_editor_cannot_disconnect_a_colleagues_nextcloud(
    connect_api, db_session, cloud, make_owner, make_user, add_member
):
    owner = await make_owner()
    started = await _start(connect_api(owner.auth), owner)
    cloud.grant()
    done = await connect_api(owner.auth).post(
        _ws(owner.workspace.id, f"login/{started['flow_id']}/poll")
    )
    connector_id = done.json()["connection"]["connector_id"]
    editor = await make_user()
    await add_member(owner.workspace, editor)

    response = await connect_api(AuthContext.session(editor)).delete(
        f"/api/v1/connectors/{connector_id}/nextcloud/connection"
    )
    assert response.status_code == 403
    assert len(await _rows(db_session, owner)) == 1
    assert cloud.revoked == []


async def test_a_granted_login_that_cannot_read_files_is_revoked_not_saved(
    connect_api, db_session, cloud, make_owner
):
    owner = await make_owner()
    client = connect_api(owner.auth)
    started = await _start(client, owner)
    password = cloud.grant()
    cloud.dav_status = 503

    response = await client.post(
        _ws(owner.workspace.id, f"login/{started['flow_id']}/poll")
    )
    assert response.status_code == 502
    assert await _rows(db_session, owner) == []
    assert cloud.revoked == [password]


async def test_starting_and_polling_are_rate_limited(connect_api, make_owner):
    owner = await make_owner()
    client = connect_api(owner.auth)
    for _ in range(config.NEXTCLOUD_LOGIN_START_LIMIT):
        assert (await client.post(_ws(owner.workspace.id, "login"))).status_code == 200
    assert (await client.post(_ws(owner.workspace.id, "login"))).status_code == 429

    other = await make_owner()
    started = await _start(connect_api(other.auth), other)
    poll = _ws(other.workspace.id, f"login/{started['flow_id']}/poll")
    for _ in range(POLL_LIMIT[0]):
        await connect_api(other.auth).post(poll)
    assert (await connect_api(other.auth).post(poll)).status_code == 429


async def test_without_a_configured_server_nothing_is_contacted(
    connect_api, make_owner, cloud, monkeypatch
):
    monkeypatch.setattr(config, "NEXTCLOUD_BASE_URL", None)
    owner = await make_owner()
    client = connect_api(owner.auth)
    status = (await client.get(_ws(owner.workspace.id, "connection"))).json()
    assert status["configured"] is False
    response = await client.post(_ws(owner.workspace.id, "login"))
    assert response.status_code == 503
    assert cloud.requests == []


async def test_a_personal_access_token_cannot_connect(
    connect_api, db_session, make_owner
):
    from app.db import PersonalAccessToken

    owner = await make_owner()
    pat = PersonalAccessToken(
        user_id=owner.user.id,
        user=owner.user,
        token_hash="1" * 64,
        token_prefix="ss_pat_test",
        label="t",
    )
    response = await connect_api(AuthContext.pat_auth(owner.user, pat)).post(
        _ws(owner.workspace.id, "login")
    )
    assert response.status_code == 403
