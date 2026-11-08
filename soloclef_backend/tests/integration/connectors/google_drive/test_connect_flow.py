"""Connecting Google Drive: consent, callback, stored grant, Picker session, disconnect.

Google is the stand-in server at the HTTP boundary; the database, the OAuth
library, the token refresh and permissions are real.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import select

from app.config import config
from app.connectors.google_drive.connect.scopes import DRIVE_FILE_SCOPE
from app.db import (
    Document,
    DocumentType,
    SearchSourceConnector,
    SearchSourceConnectorEnablement,
    SearchSourceConnectorType,
)
from app.utils.oauth_security import TokenEncryption

from .conftest import REDIRECT_URI, connect, consent

pytestmark = pytest.mark.integration


def _ws(owner, tail: str) -> str:
    return f"/api/v1/workspaces/{owner.workspace.id}/google-drive/{tail}"


async def _drive_rows(db_session, owner):
    return (
        await db_session.scalars(
            select(SearchSourceConnector).where(
                SearchSourceConnector.user_id == owner.user.id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
            )
        )
    ).all()


async def test_a_configured_server_reports_no_connection_yet(api, make_owner):
    owner = await make_owner()
    response = await api(owner.auth).get(_ws(owner, "connection"))
    assert response.status_code == 200
    assert response.json() == {
        "configured": True,
        "missing": [],
        "connector_id": None,
        "account_email": None,
        "needs_reconnect": False,
    }


async def test_an_unconfigured_server_names_what_is_missing(
    api, make_owner, monkeypatch
):
    monkeypatch.setattr(config, "GOOGLE_PICKER_API_KEY", None)
    owner = await make_owner()
    client = api(owner.auth)
    status = (await client.get(_ws(owner, "connection"))).json()
    assert status["configured"] is False
    assert status["missing"] == ["GOOGLE_PICKER_API_KEY"]
    started = await client.post(_ws(owner, "connect"), json={})
    assert started.status_code == 503


async def test_consent_asks_for_drive_file_only_with_pkce_and_offline_access(
    api, make_owner
):
    owner = await make_owner()
    started = await api(owner.auth).post(_ws(owner, "connect"), json={})
    url = started.json()["auth_url"]
    assert "scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdrive.file&" in url
    assert "access_type=offline" in url
    assert "prompt=consent" in url
    assert "code_challenge_method=S256" in url
    assert f"redirect_uri={REDIRECT_URI.replace(':', '%3A').replace('/', '%2F')}" in url


async def test_connecting_stores_an_encrypted_grant_and_returns_to_the_page(
    api, db_session, google, make_owner
):
    owner = await make_owner()
    back = await connect(
        api(owner.auth), owner, google, return_path="/dashboard/1/notebooks/2?tab=x"
    )
    assert back.status_code == 307
    assert (
        back.headers["location"]
        == "http://app.test/dashboard/1/notebooks/2?tab=x&google_drive=connected"
    )

    [row] = await _drive_rows(db_session, owner)
    cfg = row.config
    assert cfg["_token_encrypted"] is True
    assert cfg["account_email"] == "e2e.drive@example.com"
    assert cfg["start_page_token"] == "100"
    assert cfg["scopes"] == [DRIVE_FILE_SCOPE]
    refresh = TokenEncryption(config.SECRET_KEY).decrypt_token(cfg["refresh_token"])
    assert refresh.startswith("1//stub-")
    assert "1//stub-" not in str(cfg)
    assert row.name == "Google Drive - e2e.drive@example.com"
    # Periodic indexing is now a per-workspace setting (the connector is
    # account-level) — the workspace connected from gets it by default.
    enablement = await db_session.scalar(
        select(SearchSourceConnectorEnablement).where(
            SearchSourceConnectorEnablement.connector_id == row.id,
            SearchSourceConnectorEnablement.workspace_id == owner.workspace.id,
        )
    )
    assert enablement.periodic_indexing_enabled is True
    assert enablement.indexing_frequency_minutes == 1440

    status = (await api(owner.auth).get(_ws(owner, "connection"))).json()
    assert status["connector_id"] == row.id
    assert status["account_email"] == "e2e.drive@example.com"


async def test_the_catalog_connect_lands_on_the_callback_page(
    api, db_session, google, make_owner
):
    owner = await make_owner()
    back = await connect(api(owner.auth), owner, google)
    [row] = await _drive_rows(db_session, owner)
    assert back.headers["location"] == (
        f"http://app.test/dashboard/{owner.workspace.id}/connectors/callback"
        f"?success=true&connector=google-drive-connector&connectorId={row.id}"
    )


async def test_a_return_path_to_another_site_is_ignored(api, google, make_owner):
    owner = await make_owner()
    back = await connect(api(owner.auth), owner, google, return_path="//evil.example/x")
    assert back.headers["location"].startswith(
        f"http://app.test/dashboard/{owner.workspace.id}/connectors/callback?"
    )


async def test_reconnecting_reuses_the_row_and_revokes_the_old_grant(
    api, db_session, google, make_owner
):
    owner = await make_owner()
    client = api(owner.auth)
    await connect(client, owner, google)
    [first] = await _drive_rows(db_session, owner)
    first.config = {**first.config, "selected_files": [{"id": "f1", "name": "a"}]}
    await db_session.flush()
    old_refresh = TokenEncryption(config.SECRET_KEY).decrypt_token(
        first.config["refresh_token"]
    )

    await connect(client, owner, google)
    rows = await _drive_rows(db_session, owner)
    assert [r.id for r in rows] == [first.id]
    assert rows[0].config["selected_files"] == [{"id": "f1", "name": "a"}]
    assert old_refresh in google.snapshot()["revoked"]


async def test_cancelling_on_googles_page_returns_with_the_reason(
    api, db_session, google, make_owner
):
    owner = await make_owner()
    client = api(owner.auth)
    started = await client.post(_ws(owner, "connect"), json={"return_path": "/n/1"})
    back_url = consent(google, started.json()["auth_url"], decision="deny")
    back = await client.get(back_url.replace("http://test", ""), follow_redirects=False)
    assert (
        back.headers["location"]
        == "http://app.test/n/1?google_drive=google_drive_oauth_denied"
    )
    assert await _drive_rows(db_session, owner) == []


async def test_a_tampered_state_is_refused(api, db_session, google, make_owner):
    owner = await make_owner()
    response = await api(owner.auth).get(
        "/api/v1/auth/google/drive/connector/callback?code=x&state=bm90LXNpZ25lZA=="
    )
    # Not this browser's connect, and no workspace to land on: the dashboard.
    assert response.status_code == 307
    assert response.headers["location"] == "http://app.test/dashboard"
    assert await _drive_rows(db_session, owner) == []


async def test_a_consent_finished_in_another_browser_saves_nothing(
    api, db_session, google, make_owner
):
    # Someone starts a connect in their own account and gets another person
    # to finish Google's consent with that person's Drive: the other browser
    # has no connect cookie, so nothing lands on the starter's account.
    owner = await make_owner()
    client = api(owner.auth)
    started = await client.post(_ws(owner, "connect"), json={})
    back = consent(google, started.json()["auth_url"])
    client.cookies.clear()  # the other browser has none of the starter's cookies
    landed = await client.get(back.replace("http://test", ""), follow_redirects=False)
    assert "google_drive_connect_expired" in landed.headers["location"]
    assert await _drive_rows(db_session, owner) == []


async def test_the_picker_session_refreshes_an_expired_token(
    api, db_session, google, make_owner
):
    owner = await make_owner()
    client = api(owner.auth)
    await connect(client, owner, google)
    [row] = await _drive_rows(db_session, owner)
    row.config = {
        **row.config,
        "expiry": (datetime.now(UTC) - timedelta(hours=1))
        .replace(tzinfo=None)
        .isoformat(),
    }
    await db_session.flush()

    response = await client.get(
        f"/api/v1/connectors/{row.id}/google-drive/picker-session"
    )
    assert response.status_code == 200, response.text
    body = response.json()
    assert body["api_key"] == "it-picker-key"
    assert body["app_id"] == "123456789012"
    assert body["access_token"].startswith("ya29.stub-")
    assert google.snapshot()["token_requests"] == [
        "authorization_code",
        "refresh_token",
    ]


async def test_only_the_owner_gets_a_picker_session(
    api, db_session, google, make_owner, add_member
):
    owner = await make_owner()
    await connect(api(owner.auth), owner, google)
    [row] = await _drive_rows(db_session, owner)
    other = await make_owner()
    await add_member(owner.workspace, other.user)
    response = await api(other.auth).get(
        f"/api/v1/connectors/{row.id}/google-drive/picker-session"
    )
    assert response.status_code == 404


async def test_a_grant_revoked_at_google_asks_to_reconnect(
    api, db_session, google, make_owner
):
    owner = await make_owner()
    client = api(owner.auth)
    await connect(client, owner, google)
    [row] = await _drive_rows(db_session, owner)
    with google.lock:
        google.refresh_tokens.clear()
    row.config = {**row.config, "expiry": "2000-01-01T00:00:00"}
    await db_session.flush()

    response = await client.get(
        f"/api/v1/connectors/{row.id}/google-drive/picker-session"
    )
    assert response.status_code == 400
    assert response.json()["detail"]["code"] == "google_drive_reconnect"
    status = (await client.get(_ws(owner, "connection"))).json()
    assert status["needs_reconnect"] is True


async def test_disconnect_revokes_at_google_and_keeps_imported_files(
    api, db_session, google, make_owner
):
    owner = await make_owner()
    client = api(owner.auth)
    await connect(client, owner, google)
    [row] = await _drive_rows(db_session, owner)
    refresh = TokenEncryption(config.SECRET_KEY).decrypt_token(
        row.config["refresh_token"]
    )
    document = Document(
        workspace_id=owner.workspace.id,
        title="kept.md",
        document_type=DocumentType.GOOGLE_DRIVE_FILE,
        content="kept",
        content_hash="kept-hash-" + str(row.id),
        unique_identifier_hash="kept-uid-" + str(row.id),
        connector_id=row.id,
    )
    db_session.add(document)
    await db_session.flush()

    response = await client.delete(
        f"/api/v1/connectors/{row.id}/google-drive/connection"
    )
    assert response.status_code == 200
    assert response.json() == {"revoked": True}
    assert refresh in google.snapshot()["revoked"]
    assert await _drive_rows(db_session, owner) == []
    await db_session.refresh(document)
    assert document.connector_id is None
