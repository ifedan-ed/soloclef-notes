"""MCP connector boundaries over HTTP, on real Postgres.

Covers: no stdio anywhere; the test endpoint needs a session, a workspace and
CONNECTORS_CREATE, is rate limited and only says "could not connect"; a
workspace Owner or Editor adds their own server on any host that resolves
publicly, while an administrator's (a site server) must also be allowlisted;
secrets are masked for every reader and survive a masked round trip; only
the creator or an administrator edits; the adder of a member server or the
Owner deletes it, and a member's own servers go when their membership ends;
URL-opening tools always ask; and the live-style connectors (Exa, Tavily,
Browserbase) keep working.
"""

from __future__ import annotations

import uuid
from collections.abc import AsyncGenerator
from unittest.mock import patch

import httpx
import pytest
import pytest_asyncio
from httpx import ASGITransport
from sqlalchemy.ext.asyncio import AsyncSession

from app.app import app, limiter
from app.auth.context import AuthContext
from app.db import (
    SearchSourceConnector,
    SearchSourceConnectorType,
    User,
    Workspace,
    get_async_session,
)
from app.mcp_security import allowlist, url_guard
from app.mcp_security.secrets import MASK
from app.routes import search_source_connectors_routes as routes
from app.users import get_auth_context

pytestmark = pytest.mark.integration

limiter.enabled = False

PUBLIC_IP = "93.184.215.14"
EXA_KEY = "exa-live-key-SECRET-0001"
BB_KEY = "Bearer bb_live_SECRET_BROWSERBASE_7777"

_PRIVATE = {
    "private10.test": ["10.1.2.3"],
    "private172.test": ["172.18.0.5"],
    "private192.test": ["192.168.0.10"],
    "loopback.test": ["127.0.0.1"],
    "linklocal.test": ["169.254.169.254"],
}


class _Who:
    """The principal the app sees; tests switch it between requests."""

    def __init__(self) -> None:
        self.auth: AuthContext | None = None


@pytest.fixture(autouse=True)
def _fake_dns(monkeypatch):
    """Allowlisted real names resolve publicly; *.test names to private ranges."""

    async def fake(host: str, port: int) -> list[str]:
        if host in _PRIVATE:
            return _PRIVATE[host]
        if host in {
            "mcp.exa.ai",
            "mcp.tavily.com",
            "mcp.browserbase.com",
            "mcp.notion.com",
        }:
            return [PUBLIC_IP]
        raise OSError("NXDOMAIN")

    monkeypatch.setattr(url_guard, "_getaddrinfo", fake)
    allowlist.reset_cache()
    routes._mcp_test_calls.clear()
    yield
    allowlist.reset_cache()


@pytest.fixture(autouse=True)
def _no_background_discovery():
    with patch(
        "app.agents.chat.multi_agent_chat.shared.tools.mcp.cache.refresh_mcp_tools_cache_for_connector"
    ):
        yield


@pytest_asyncio.fixture
async def who() -> _Who:
    return _Who()


@pytest_asyncio.fixture
async def client(
    db_session: AsyncSession, who: _Who
) -> AsyncGenerator[httpx.AsyncClient, None]:
    async def override_session() -> AsyncGenerator[AsyncSession, None]:
        yield db_session

    async def override_auth() -> AuthContext:
        assert who.auth is not None
        return who.auth

    previous = app.dependency_overrides.copy()
    app.dependency_overrides[get_async_session] = override_session
    app.dependency_overrides[get_auth_context] = override_auth
    try:
        async with httpx.AsyncClient(
            transport=ASGITransport(app=app), base_url="http://test", timeout=30.0
        ) as c:
            yield c
    finally:
        app.dependency_overrides.clear()
        app.dependency_overrides.update(previous)


@pytest_asyncio.fixture
async def admin(db_session: AsyncSession, db_workspace: Workspace, add_member) -> User:
    user = User(
        id=uuid.uuid4(),
        email=f"admin-{uuid.uuid4().hex[:6]}@surfsense.net",
        hashed_password="x",
        is_active=True,
        is_superuser=True,  # is_config_admin: superuser or BILLING_ADMIN_EMAILS
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    await add_member(db_workspace, user, "Editor")
    return user


async def _member(make_user, add_member, workspace, role) -> User:
    user = await make_user()
    await add_member(workspace, user, role)
    return user


async def _row(session, workspace, owner, name, server_config, **extra):
    connector = SearchSourceConnector(
        name=name,
        connector_type=SearchSourceConnectorType.MCP_CONNECTOR,
        is_indexable=False,
        config={"server_config": server_config, **extra},
        workspace_id=workspace.id,
        user_id=owner.id,
    )
    session.add(connector)
    await session.flush()
    return connector


EXA_CONFIG = {
    "url": "https://mcp.exa.ai/mcp",
    "headers": {"x-api-key": EXA_KEY},
    "transport": "streamable-http",
}
STDIO_CONFIG = {
    "command": "python",
    "args": ["-c", "open('/tmp/pwned','w').write('x')"],
    "transport": "stdio",
}


# --------------------------------------------------------------------------
# Test endpoint
# --------------------------------------------------------------------------


class TestTestEndpoint:
    async def test_refuses_personal_access_tokens(
        self, client, who, db_user, db_workspace
    ):
        who.auth = AuthContext(user=db_user, method="pat", pat=None)
        r = await client.post(
            f"/api/v1/connectors/mcp/test?workspace_id={db_workspace.id}",
            json=EXA_CONFIG,
        )
        assert r.status_code == 403

    async def test_requires_a_workspace(self, client, who, db_user):
        who.auth = AuthContext.session(db_user)
        r = await client.post("/api/v1/connectors/mcp/test", json=EXA_CONFIG)
        assert r.status_code == 422

    async def test_requires_membership(self, client, who, make_user, db_workspace):
        who.auth = AuthContext.session(await make_user())
        r = await client.post(
            f"/api/v1/connectors/mcp/test?workspace_id={db_workspace.id}",
            json=EXA_CONFIG,
        )
        assert r.status_code == 403

    async def test_viewer_cannot_test(
        self, client, who, make_user, add_member, db_workspace
    ):
        who.auth = AuthContext.session(
            await _member(make_user, add_member, db_workspace, "Viewer")
        )
        r = await client.post(
            f"/api/v1/connectors/mcp/test?workspace_id={db_workspace.id}",
            json=EXA_CONFIG,
        )
        assert r.status_code == 403

    async def test_stdio_is_refused_and_nothing_runs(
        self, client, who, db_user, db_workspace
    ):
        who.auth = AuthContext.session(db_user)
        with patch("anyio.open_process") as spawn:
            r = await client.post(
                f"/api/v1/connectors/mcp/test?workspace_id={db_workspace.id}",
                json=STDIO_CONFIG,
            )
        assert r.status_code == 422
        assert "stdio" in r.json()["detail"].lower()
        spawn.assert_not_called()

    @pytest.mark.parametrize(
        "url",
        [
            "https://litellm:4000/",
            "http://litellm:4000/",
            "https://127.0.0.1/mcp",
            "https://10.0.0.1/mcp",
            "https://172.16.0.1/mcp",
            "https://192.168.1.1/mcp",
            "https://169.254.169.254/latest/meta-data/",
            "https://private10.test/mcp",
            "https://private172.test/mcp",
            "https://private192.test/mcp",
            "https://loopback.test/mcp",
            "https://linklocal.test/mcp",
            "https://example.com/mcp",
        ],
    )
    async def test_ssrf_targets_get_only_a_generic_failure(
        self, client, who, db_user, db_workspace, db_session, url
    ):
        # Put the private-resolving names on the allowlist, so the refusal
        # comes from the address check and not just the list.
        await allowlist.save_allowed_hosts(
            db_session,
            [*allowlist.DEFAULT_ALLOWED_HOSTS, *_PRIVATE],
            actor=None,
        )
        who.auth = AuthContext.session(db_user)
        with patch(
            "app.agents.chat.multi_agent_chat.shared.tools.mcp.client.streamablehttp_client"
        ) as connect:
            r = await client.post(
                f"/api/v1/connectors/mcp/test?workspace_id={db_workspace.id}",
                json={"url": url, "transport": "streamable-http"},
            )
        assert r.status_code == 200
        body = r.json()
        assert body == {
            "status": "error",
            "message": "Could not connect to the MCP server. Check the URL and key.",
            "tools": [],
        }
        connect.assert_not_called()

    async def test_connection_error_text_is_not_returned(
        self, client, who, db_user, db_workspace
    ):
        who.auth = AuthContext.session(db_user)

        def boom(*args, **kwargs):
            raise RuntimeError("secret internal detail: redis://redis:6379")

        with patch(
            "app.agents.chat.multi_agent_chat.shared.tools.mcp.client.streamablehttp_client",
            side_effect=boom,
        ):
            r = await client.post(
                f"/api/v1/connectors/mcp/test?workspace_id={db_workspace.id}",
                json=EXA_CONFIG,
            )
        body = r.json()
        assert body["status"] == "error"
        assert "redis" not in body["message"]
        assert body["tools"] == []

    async def test_is_rate_limited(self, client, who, db_user, db_workspace):
        who.auth = AuthContext.session(db_user)
        url = f"/api/v1/connectors/mcp/test?workspace_id={db_workspace.id}"
        body = {"url": "https://example.com/mcp"}
        codes = [(await client.post(url, json=body)).status_code for _ in range(11)]
        assert codes[:10] == [200] * 10
        assert codes[10] == 429


# --------------------------------------------------------------------------
# Adding servers
# --------------------------------------------------------------------------


NOTION_CONFIG = {
    "url": "https://mcp.notion.com/mcp",
    "headers": {"Authorization": "Bearer ntn_own-key-SECRET-9999"},
    "transport": "streamable-http",
}


class TestCreate:
    async def test_workspace_owner_adds_their_own_server_off_the_list(
        self, client, who, db_user, db_workspace, db_session
    ):
        who.auth = AuthContext.session(db_user)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Notion", "server_config": NOTION_CONFIG},
        )
        assert r.status_code == 201, r.text
        assert "SECRET" not in r.text
        row = await db_session.get(SearchSourceConnector, r.json()["id"])
        assert row.config["server_origin"] == "member"
        assert row.config["server_config"]["headers"]["Authorization"].endswith("9999")

    async def test_editor_adds_and_viewer_cannot(
        self, client, who, make_user, add_member, db_workspace
    ):
        who.auth = AuthContext.session(
            await _member(make_user, add_member, db_workspace, "Editor")
        )
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Notion", "server_config": NOTION_CONFIG},
        )
        assert r.status_code == 201, r.text
        who.auth = AuthContext.session(
            await _member(make_user, add_member, db_workspace, "Viewer")
        )
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Notion", "server_config": NOTION_CONFIG},
        )
        assert r.status_code == 403

    async def test_non_member_cannot_add(self, client, who, make_user, db_workspace):
        who.auth = AuthContext.session(await make_user())
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Notion", "server_config": NOTION_CONFIG},
        )
        assert r.status_code == 403

    @pytest.mark.parametrize(
        "url",
        [
            "https://litellm:4000/",
            "https://litellm/mcp",
            "https://backend/mcp",
            "https://localhost/mcp",
            "https://127.0.0.1/",
            "https://169.254.169.254/latest/meta-data/",
            "https://private10.test/mcp",
            "https://private172.test/mcp",
            "https://private192.test/mcp",
            "https://loopback.test/mcp",
            "https://linklocal.test/mcp",
            "https://example.com/mcp",  # does not resolve
            "http://mcp.notion.com/mcp",
            "https://mcp.notion.com:8443/mcp",
            "https://user:pw@mcp.notion.com/mcp",
        ],
    )
    async def test_member_cannot_point_at_internal_hosts(
        self, client, who, db_user, db_workspace, url
    ):
        who.auth = AuthContext.session(db_user)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "x", "server_config": {"url": url}},
        )
        assert r.status_code == 400, (url, r.text)

    async def test_member_pat_cannot_add(self, client, who, db_user, db_workspace):
        who.auth = AuthContext(user=db_user, method="pat", pat=None)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Notion", "server_config": NOTION_CONFIG},
        )
        assert r.status_code == 403

    async def test_agent_sign_in_cannot_add(self, client, who, db_user, db_workspace):
        who.auth = AuthContext.agent(db_user)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Notion", "server_config": NOTION_CONFIG},
        )
        assert r.status_code == 403

    async def test_admins_site_server_must_be_on_the_list(
        self, client, who, admin, db_workspace, db_session
    ):
        who.auth = AuthContext.session(admin)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Notion", "server_config": NOTION_CONFIG},
        )
        assert r.status_code == 400
        assert "allowed list" in r.json()["detail"]

    async def test_admin_pat_cannot_add(self, client, who, admin, db_workspace):
        who.auth = AuthContext(user=admin, method="pat", pat=None)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Exa", "server_config": EXA_CONFIG},
        )
        assert r.status_code == 403

    async def test_stdio_is_refused_on_create(self, client, who, admin, db_workspace):
        who.auth = AuthContext.session(admin)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "x", "server_config": STDIO_CONFIG},
        )
        assert r.status_code == 422

    @pytest.mark.parametrize(
        "url",
        [
            "https://litellm:4000/",
            "https://127.0.0.1/",
            "https://private10.test/mcp",
            "https://private172.test/mcp",
            "https://private192.test/mcp",
            "https://loopback.test/mcp",
            "https://linklocal.test/mcp",
            "https://example.com/mcp",
            "http://mcp.exa.ai/mcp",
        ],
    )
    async def test_admin_cannot_point_at_internal_or_unlisted_hosts(
        self, client, who, admin, db_workspace, db_session, url
    ):
        await allowlist.save_allowed_hosts(
            db_session, [*allowlist.DEFAULT_ALLOWED_HOSTS, *_PRIVATE], actor=None
        )
        who.auth = AuthContext.session(admin)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "x", "server_config": {"url": url}},
        )
        assert r.status_code == 400

    async def test_admin_adds_allowlisted_server_and_sees_it_masked(
        self, client, who, admin, db_workspace, db_session
    ):
        who.auth = AuthContext.session(admin)
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "Exa", "server_config": EXA_CONFIG},
        )
        assert r.status_code == 201, r.text
        assert r.json()["server_config"]["headers"]["x-api-key"] == MASK + "0001"
        row = await db_session.get(SearchSourceConnector, r.json()["id"])
        assert row.config["server_config"]["headers"]["x-api-key"] == EXA_KEY
        assert row.config["server_origin"] == "site"

        # The research web search box still reads the real key server-side.
        from app.notebooks.research.web_apis.keys import workspace_keys

        assert (await workspace_keys(db_session, db_workspace.id))["exa"] == EXA_KEY

    async def test_generic_route_cannot_create_mcp(
        self, client, who, admin, db_workspace
    ):
        who.auth = AuthContext.session(admin)
        for body in (
            {
                "name": "x",
                "connector_type": "MCP_CONNECTOR",
                "is_indexable": False,
                "config": {"server_config": STDIO_CONFIG},
            },
            {
                "name": "x",
                "connector_type": "GOOGLE_DRIVE_CONNECTOR",
                "is_indexable": False,
                "config": {"server_config": STDIO_CONFIG},
            },
        ):
            r = await client.post(
                f"/api/v1/search-source-connectors?workspace_id={db_workspace.id}",
                json=body,
            )
            assert r.status_code == 400, r.text

    async def test_admin_edits_the_allowlist_and_others_cannot(
        self, client, who, admin, db_user, db_workspace
    ):
        who.auth = AuthContext.session(db_user)
        assert (await client.get("/api/v1/admin/mcp/allowed-hosts")).status_code == 403
        assert (
            await client.get(
                f"/api/v1/connectors/mcp/policy?workspace_id={db_workspace.id}"
            )
        ).json() == {"can_add": True, "allow_list_applies": False}
        who.auth = AuthContext.session(admin)
        assert (await client.get("/api/v1/connectors/mcp/policy")).json() == {
            "can_add": True,
            "allow_list_applies": True,
        }
        r = await client.get("/api/v1/admin/mcp/allowed-hosts")
        assert set(r.json()["hosts"]) == set(allowlist.DEFAULT_ALLOWED_HOSTS)
        r = await client.put(
            "/api/v1/admin/mcp/allowed-hosts", json={"hosts": ["https://x/ bad"]}
        )
        assert r.status_code == 422
        r = await client.put(
            "/api/v1/admin/mcp/allowed-hosts", json={"hosts": ["mcp.exa.ai"]}
        )
        assert r.json() == {"hosts": ["mcp.exa.ai"]}
        # Tavily is no longer allowed.
        r = await client.post(
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            json={"name": "t", "server_config": {"url": "https://mcp.tavily.com/mcp/"}},
        )
        assert r.status_code == 400


# --------------------------------------------------------------------------
# Reading and editing
# --------------------------------------------------------------------------


class TestReadAndEdit:
    async def test_viewer_sees_masked_secrets_everywhere(
        self, client, who, db_session, db_user, db_workspace, make_user, add_member
    ):
        row = await _row(
            db_session,
            db_workspace,
            db_user,
            "Browserbase",
            {
                "url": "https://mcp.browserbase.com/mcp?apiKey=bb-url-SECRET-4242",
                "headers": {"Authorization": BB_KEY},
                "transport": "streamable-http",
            },
        )
        who.auth = AuthContext.session(
            await _member(make_user, add_member, db_workspace, "Viewer")
        )
        for path in (
            f"/api/v1/connectors/mcp?workspace_id={db_workspace.id}",
            f"/api/v1/connectors/mcp/{row.id}",
            f"/api/v1/search-source-connectors?workspace_id={db_workspace.id}",
            f"/api/v1/search-source-connectors/{row.id}",
        ):
            r = await client.get(path)
            assert r.status_code == 200, (path, r.text)
            assert "SECRET" not in r.text, path
            assert MASK in r.text, path

    async def test_viewer_policy_says_cannot_add(
        self, client, who, make_user, add_member, db_workspace
    ):
        who.auth = AuthContext.session(
            await _member(make_user, add_member, db_workspace, "Viewer")
        )
        r = await client.get(
            f"/api/v1/connectors/mcp/policy?workspace_id={db_workspace.id}"
        )
        assert r.json() == {"can_add": False, "allow_list_applies": False}

    async def test_members_server_can_move_to_another_public_host_not_inward(
        self, client, who, db_session, db_user, db_workspace
    ):
        row = await _row(
            db_session,
            db_workspace,
            db_user,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
        )
        who.auth = AuthContext.session(db_user)
        r = await client.put(
            f"/api/v1/connectors/mcp/{row.id}",
            json={"server_config": {"url": "https://mcp.tavily.com/mcp/"}},
        )
        assert r.status_code == 200, r.text
        for url in ("https://litellm:4000/", "https://private10.test/mcp"):
            r = await client.put(
                f"/api/v1/search-source-connectors/{row.id}",
                json={"config": {"server_config": {"url": url}}},
            )
            assert r.status_code == 400, url
        await db_session.refresh(row)
        assert row.config["server_origin"] == "member"

    async def test_editor_who_did_not_add_it_cannot_edit(
        self, client, who, db_session, admin, db_workspace, make_user, add_member
    ):
        row = await _row(db_session, db_workspace, admin, "Exa", EXA_CONFIG)
        who.auth = AuthContext.session(
            await _member(make_user, add_member, db_workspace, "Editor")
        )
        r = await client.put(
            f"/api/v1/connectors/mcp/{row.id}",
            json={"server_config": {"url": "https://mcp.tavily.com/mcp/"}},
        )
        assert r.status_code == 403
        r = await client.put(
            f"/api/v1/search-source-connectors/{row.id}",
            json={"config": {"server_config": {"url": "https://mcp.tavily.com/mcp/"}}},
        )
        assert r.status_code == 403

    async def test_creator_round_trips_masked_key(
        self, client, who, db_session, db_user, db_workspace
    ):
        row = await _row(db_session, db_workspace, db_user, "Exa", EXA_CONFIG)
        who.auth = AuthContext.session(db_user)
        read = (await client.get(f"/api/v1/connectors/mcp/{row.id}")).json()
        r = await client.put(
            f"/api/v1/connectors/mcp/{row.id}",
            json={"name": "Exa search", "server_config": read["server_config"]},
        )
        assert r.status_code == 200, r.text
        await db_session.refresh(row)
        assert row.config["server_config"]["headers"]["x-api-key"] == EXA_KEY

        # The generic update path (the edit dialog) behaves the same.
        r = await client.put(
            f"/api/v1/search-source-connectors/{row.id}",
            json={"config": {"server_config": read["server_config"]}},
        )
        assert r.status_code == 200, r.text
        await db_session.refresh(row)
        assert row.config["server_config"]["headers"]["x-api-key"] == EXA_KEY

    async def test_masked_key_cannot_be_moved_to_another_host(
        self, client, who, db_session, db_user, db_workspace
    ):
        row = await _row(db_session, db_workspace, db_user, "Exa", EXA_CONFIG)
        who.auth = AuthContext.session(db_user)
        r = await client.put(
            f"/api/v1/connectors/mcp/{row.id}",
            json={
                "server_config": {
                    "url": "https://mcp.tavily.com/mcp/",
                    "headers": {"x-api-key": MASK + "0001"},
                }
            },
        )
        assert r.status_code == 422

    async def test_update_rejects_stdio_and_private_hosts(
        self, client, who, db_session, db_user, db_workspace
    ):
        row = await _row(db_session, db_workspace, db_user, "Exa", EXA_CONFIG)
        who.auth = AuthContext.session(db_user)
        r = await client.put(
            f"/api/v1/connectors/mcp/{row.id}", json={"server_config": STDIO_CONFIG}
        )
        assert r.status_code == 422
        r = await client.put(
            f"/api/v1/search-source-connectors/{row.id}",
            json={"config": {"server_config": STDIO_CONFIG}},
        )
        assert r.status_code == 422
        r = await client.put(
            f"/api/v1/search-source-connectors/{row.id}",
            json={"config": {"server_config": {"url": "https://litellm:4000/"}}},
        )
        assert r.status_code == 400
        r = await client.put(
            f"/api/v1/search-source-connectors/{row.id}",
            json={"connector_type": "GOOGLE_DRIVE_CONNECTOR"},
        )
        assert r.status_code == 400
        await db_session.refresh(row)
        assert row.config["server_config"] == EXA_CONFIG

    async def test_other_connector_cannot_become_mcp(
        self, client, who, db_session, db_user, db_connector
    ):
        who.auth = AuthContext.session(db_user)
        r = await client.put(
            f"/api/v1/search-source-connectors/{db_connector.id}",
            json={"config": {"server_config": STDIO_CONFIG}},
        )
        assert r.status_code == 400
        r = await client.put(
            f"/api/v1/search-source-connectors/{db_connector.id}",
            json={"connector_type": "MCP_CONNECTOR"},
        )
        assert r.status_code == 400

    async def test_url_opening_tool_cannot_be_trusted(
        self, client, who, db_session, db_user, db_workspace
    ):
        row = await _row(db_session, db_workspace, db_user, "Browserbase", EXA_CONFIG)
        who.auth = AuthContext.session(db_user)
        r = await client.post(
            f"/api/v1/connectors/mcp/{row.id}/trust-tool",
            json={"tool_name": "navigate"},
        )
        assert r.status_code == 400
        r = await client.post(
            f"/api/v1/connectors/mcp/{row.id}/trust-tool", json={"tool_name": "observe"}
        )
        assert r.status_code == 200
        assert r.json()["trusted_tools"] == ["observe"]


# --------------------------------------------------------------------------
# Agent build
# --------------------------------------------------------------------------


def _cached(names: list[str]) -> dict:
    return {
        "tools": [
            {"name": n, "description": n, "input_schema": {"type": "object"}}
            for n in names
        ],
        "discovered_at": "2026-09-21T12:00:00+00:00",
        "server_name": "srv",
        "server_version": "1",
        "transport": "streamable-http",
    }


# --------------------------------------------------------------------------
# Removing servers, and a member's own servers when their membership ends
# --------------------------------------------------------------------------


async def _second_workspace(session, user) -> None:
    """Somewhere else to be, so leaving is not refused as the last one."""
    from app.routes.workspaces_routes import create_default_roles_and_membership

    elsewhere = Workspace(name="Elsewhere", user_id=user.id)
    session.add(elsewhere)
    await session.flush()
    await create_default_roles_and_membership(session, elsewhere.id, user.id)
    await session.flush()


async def _gone(session, *rows) -> list[bool]:
    ids = [row.id for row in rows]
    session.expunge_all()
    return [await session.get(SearchSourceConnector, i) is None for i in ids]


class TestRemove:
    @pytest.mark.parametrize(
        "path", ["/api/v1/connectors/mcp/{id}", "/api/v1/search-source-connectors/{id}"]
    )
    async def test_editor_removes_their_own_server(
        self, client, who, db_session, db_workspace, make_user, add_member, path
    ):
        editor = await _member(make_user, add_member, db_workspace, "Editor")
        row = await _row(
            db_session,
            db_workspace,
            editor,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
        )
        who.auth = AuthContext.session(editor)
        r = await client.delete(path.format(id=row.id))
        assert r.status_code in (200, 204), r.text
        assert await _gone(db_session, row) == [True]

    async def test_editor_cannot_remove_someone_elses_or_a_site_server(
        self,
        client,
        who,
        db_session,
        db_user,
        admin,
        db_workspace,
        make_user,
        add_member,
    ):
        owners = await _row(
            db_session,
            db_workspace,
            db_user,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
        )
        site = await _row(db_session, db_workspace, admin, "Exa", EXA_CONFIG)
        who.auth = AuthContext.session(
            await _member(make_user, add_member, db_workspace, "Editor")
        )
        for row in (owners, site):
            r = await client.delete(f"/api/v1/connectors/mcp/{row.id}")
            assert r.status_code == 403
        # The administrator who added the site server is an Editor here: the
        # site's own services stay the Owner's to remove.
        who.auth = AuthContext.session(admin)
        r = await client.delete(f"/api/v1/connectors/mcp/{site.id}")
        assert r.status_code == 403
        assert await _gone(db_session, owners, site) == [False, False]

    async def test_owner_still_removes_any_server(
        self, client, who, db_session, db_user, db_workspace, make_user, add_member
    ):
        editor = await _member(make_user, add_member, db_workspace, "Editor")
        row = await _row(
            db_session,
            db_workspace,
            editor,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
        )
        who.auth = AuthContext.session(db_user)
        r = await client.delete(f"/api/v1/connectors/mcp/{row.id}")
        assert r.status_code == 204, r.text

    async def test_leaving_deletes_only_your_own_member_servers(
        self,
        client,
        who,
        db_session,
        db_user,
        admin,
        db_workspace,
        make_user,
        add_member,
    ):
        editor = await _member(make_user, add_member, db_workspace, "Editor")
        await _second_workspace(db_session, editor)
        mine = await _row(
            db_session,
            db_workspace,
            editor,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
        )
        # A row from before the split (no marker) counts as a site server.
        old = await _row(db_session, db_workspace, editor, "Exa", EXA_CONFIG)
        owners = await _row(
            db_session,
            db_workspace,
            db_user,
            "Notion 2",
            NOTION_CONFIG,
            server_origin="member",
        )
        site = await _row(
            db_session, db_workspace, admin, "Tavily", EXA_CONFIG, server_origin="site"
        )
        who.auth = AuthContext.session(editor)
        with patch(
            "app.agents.chat.multi_agent_chat.shared.tools.mcp.tool.invalidate_mcp_tools_cache"
        ) as forget:
            r = await client.delete(f"/api/v1/workspaces/{db_workspace.id}/members/me")
        assert r.status_code == 200, r.text
        forget.assert_called_once_with(db_workspace.id)
        assert await _gone(db_session, mine, old, owners, site) == [
            True,
            False,
            False,
            False,
        ]

    async def test_being_removed_deletes_their_member_servers(
        self, client, who, db_session, db_user, db_workspace, make_user, add_member
    ):
        editor = await make_user()
        membership = await add_member(db_workspace, editor, "Editor")
        theirs = await _row(
            db_session,
            db_workspace,
            editor,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
        )
        who.auth = AuthContext.session(db_user)
        r = await client.delete(
            f"/api/v1/workspaces/{db_workspace.id}/members/{membership.id}"
        )
        assert r.status_code == 200, r.text
        assert await _gone(db_session, theirs) == [True]

    async def test_a_refused_leave_keeps_the_servers(
        self, client, who, db_session, db_workspace, make_user, add_member
    ):
        editor = await _member(make_user, add_member, db_workspace, "Editor")
        theirs = await _row(
            db_session,
            db_workspace,
            editor,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
        )
        who.auth = AuthContext.session(editor)
        r = await client.delete(f"/api/v1/workspaces/{db_workspace.id}/members/me")
        assert r.status_code == 409  # their only workspace
        assert await _gone(db_session, theirs) == [False]


class TestAgentBuild:
    async def test_stdio_rows_never_start_and_live_rows_still_load(
        self, db_session, db_user, db_workspace, caplog
    ):
        from app.agents.chat.multi_agent_chat.shared.tools.mcp import tool as mcp_tool
        from app.agents.chat.multi_agent_chat.shared.tools.mcp.cache import (
            read_cached_tools,
        )

        stdio = await _row(db_session, db_workspace, db_user, "Evil", STDIO_CONFIG)
        await _row(
            db_session,
            db_workspace,
            db_user,
            "Internal",
            {"url": "https://litellm:4000/", "transport": "streamable-http"},
        )
        bb = await _row(
            db_session,
            db_workspace,
            db_user,
            "Browserbase",
            {
                "url": "https://mcp.browserbase.com/mcp",
                "headers": {"Authorization": BB_KEY},
                "transport": "streamable-http",
            },
            trusted_tools=["start", "navigate", "extract", "observe", "end"],
            cached_tools=_cached(
                ["start", "end", "navigate", "act", "observe", "extract"]
            ),
        )
        assert read_cached_tools(bb) is not None

        mcp_tool.invalidate_mcp_tools_cache()
        with patch("anyio.open_process") as spawn, caplog.at_level("WARNING"):
            tools = await mcp_tool.load_mcp_tools(db_session, db_workspace.id)
        spawn.assert_not_called()
        names = sorted(t.name for t in tools)
        assert names == ["act", "end", "extract", "navigate", "observe", "start"]
        assert any(
            str(stdio.id) in rec.getMessage() and "not supported" in rec.getMessage()
            for rec in caplog.records
        )

    async def test_a_members_own_server_loads_off_the_list_but_never_inward(
        self, db_session, db_user, db_workspace
    ):
        from app.agents.chat.multi_agent_chat.shared.tools.mcp import tool as mcp_tool

        await _row(
            db_session,
            db_workspace,
            db_user,
            "Notion",
            NOTION_CONFIG,
            server_origin="member",
            cached_tools=_cached(["notion_search"]),
        )
        await _row(
            db_session,
            db_workspace,
            db_user,
            "Sneaky",
            {"url": "https://litellm:4000/", "transport": "streamable-http"},
            server_origin="member",
            cached_tools=_cached(["sneaky"]),
        )
        # The same host as a site server (no marker) is held to the list.
        await _row(
            db_session,
            db_workspace,
            db_user,
            "Unlisted",
            {"url": "https://mcp.notion.com/other", "transport": "streamable-http"},
            cached_tools=_cached(["unlisted"]),
        )
        mcp_tool.invalidate_mcp_tools_cache()
        tools = await mcp_tool.load_mcp_tools(db_session, db_workspace.id)
        assert sorted(t.name for t in tools) == ["notion_search"]

    async def test_trusted_navigate_still_asks(self, db_session, db_user, db_workspace):
        from app.services.user_tool_allowlist import fetch_user_allowlist_rulesets

        await _row(
            db_session,
            db_workspace,
            db_user,
            "Browserbase",
            {"url": "https://mcp.browserbase.com/mcp", "transport": "streamable-http"},
            trusted_tools=["start", "navigate", "observe"],
        )
        rulesets = await fetch_user_allowlist_rulesets(
            db_session, user_id=db_user.id, workspace_id=db_workspace.id
        )
        allowed = {r.permission for rs in rulesets.values() for r in rs.rules}
        assert "navigate" not in allowed
        assert {"start", "observe"} <= allowed

    async def test_call_time_check_refuses_a_host_removed_from_the_list(
        self, db_session, db_user, db_workspace
    ):
        from app.agents.chat.multi_agent_chat.shared.tools.mcp import tool as mcp_tool

        built = await mcp_tool._create_mcp_tool_from_definition_http(
            {"name": "web_search_exa", "description": "d", "input_schema": {}},
            "https://mcp.exa.ai/mcp",
            {"x-api-key": EXA_KEY},
            connector_name="Exa",
            connector_id=1,
            bypass_internal_hitl=True,
        )
        await allowlist.save_allowed_hosts(db_session, ["mcp.tavily.com"], actor=None)
        with patch(
            "app.agents.chat.multi_agent_chat.shared.tools.mcp.tool.streamablehttp_client"
        ) as connect:
            out = await built.coroutine(query="x")
        connect.assert_not_called()
        assert "not available" in out
