"""Connecting Google Drive from the phone app, through the browser hand-off.

The app asks for a code with its token and its PKCE challenge, the browser
sheet opens the code's page, Google's (stand-in) consent runs, and the
callback holds the grant and sends the sheet to the app's own address with a
completion; the app saves the grant with its token and verifier
(``POST /app/handoff/complete``), as the website's connect would have. The
database, the permissions, the OAuth library and the cookie signing are
real; only Google is the loopback stand-in (conftest.py).
"""

from __future__ import annotations

import base64
import hashlib
from types import SimpleNamespace
from urllib.parse import parse_qs, urlsplit

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI
from sqlalchemy import select

from app.app_handoff import routes as handoff_routes
from app.app_handoff.codes import (
    COMPLETION_LIFETIME_SECONDS,
    MemoryHandoffCodes,
    MemoryStore,
    completion_store,
)
from app.config import config
from app.connectors.email_imap.microsoft import routes as microsoft_routes
from app.connectors.email_imap.microsoft.settings import MicrosoftMailSettings
from app.connectors.google_drive.connect import routes as connect_routes
from app.db import (
    SearchSourceConnector,
    SearchSourceConnectorType,
    get_async_session,
)
from app.users import get_auth_context, require_session_context

from .conftest import REDIRECT_URI, consent

pytestmark = pytest.mark.integration

APP = "soloclef://connectors/done"
# The app's own PKCE pair (RFC 7636's example verifier).
VERIFIER = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
CHALLENGE = (
    base64.urlsafe_b64encode(hashlib.sha256(VERIFIER.encode()).digest())
    .rstrip(b"=")
    .decode()
)


@pytest_asyncio.fixture
async def phone(db_session, google, monkeypatch):
    monkeypatch.setattr(config, "APP_HANDOFF_RETURN", APP, raising=False)
    app = FastAPI()
    app.include_router(connect_routes.router, prefix="/api/v1")
    app.include_router(handoff_routes.router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)
    codes = MemoryHandoffCodes()
    completions = MemoryStore(COMPLETION_LIFETIME_SECONDS)

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[require_session_context] = lambda: acting.auth
    app.dependency_overrides[handoff_routes.get_handoff_codes] = lambda: codes
    app.dependency_overrides[completion_store] = lambda: completions
    app.dependency_overrides[microsoft_routes.get_microsoft_mail_settings] = lambda: (
        MicrosoftMailSettings()
    )
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://test"
    ) as client:

        def as_user(auth):
            acting.auth = auth
            return client

        yield as_user


async def _sheet_returns(client, owner, google, decision="allow"):
    """The app's code, the sheet's page, Google's consent, the callback: where the sheet lands."""
    started = await client.post(
        "/api/v1/app/handoff",
        json={
            "purpose": "drive_connect",
            "workspace_id": owner.workspace.id,
            "code_challenge": CHALLENGE,
        },
    )
    assert started.status_code == 200, started.text
    page = await client.get(
        f"/api/v1/app/handoff/{started.json()['code']}", follow_redirects=False
    )
    assert page.status_code == 303
    back = consent(google, page.headers["location"], decision=decision)
    parts = urlsplit(back)
    assert f"{parts.scheme}://{parts.netloc}{parts.path}" == REDIRECT_URI
    landed = await client.get(f"{parts.path}?{parts.query}", follow_redirects=False)
    assert landed.status_code == 303
    target = urlsplit(landed.headers["location"])
    assert f"{target.scheme}://{target.netloc}{target.path}" == APP
    return {k: v[0] for k, v in parse_qs(target.query).items()}


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


async def test_the_app_connects_drive_once_it_confirms(
    phone, db_session, google, make_owner
):
    owner = await make_owner()
    client = phone(owner.auth)
    landed = await _sheet_returns(client, owner, google)
    assert landed["purpose"] == "drive_connect" and landed["status"] == "confirm"
    # Held, not saved, until the app confirms.
    assert await _drive_rows(db_session, owner) == []
    done = await client.post(
        "/api/v1/app/handoff/complete",
        json={"completion": landed["completion"], "code_verifier": VERIFIER},
    )
    assert done.status_code == 200, done.text
    assert done.json() == {"purpose": "drive_connect", "status": "connected"}
    [row] = await _drive_rows(db_session, owner)
    assert row.config["account_email"] == "e2e.drive@example.com"
    assert row.workspace_id is None  # account-wide, as the website's connect makes it


async def test_cancelling_on_googles_page_comes_back_to_the_app_with_the_reason(
    phone, db_session, google, make_owner
):
    owner = await make_owner()
    landed = await _sheet_returns(phone(owner.auth), owner, google, decision="deny")
    assert landed == {
        "purpose": "drive_connect",
        "status": "google_drive_oauth_denied",
    }
    assert await _drive_rows(db_session, owner) == []


async def test_someone_outside_the_workspace_gets_no_code(phone, make_owner):
    owner = await make_owner()
    stranger = await make_owner()
    res = await phone(stranger.auth).post(
        "/api/v1/app/handoff",
        json={
            "purpose": "drive_connect",
            "workspace_id": owner.workspace.id,
            "code_challenge": CHALLENGE,
        },
    )
    assert res.status_code in (403, 404)
