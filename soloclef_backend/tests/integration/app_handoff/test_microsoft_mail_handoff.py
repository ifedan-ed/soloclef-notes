"""The phone app connects a Microsoft mailbox through the browser hand-off, on real Postgres.

The hand-off page takes no credentials: whoever opens a code's link goes
through Microsoft's consent in their own browser. So the callback holds the
mailbox, and only the app that asked for the code saves it, with its bearer
token and the PKCE verifier behind the challenge it asked with. Here a code
is opened by a second browser (someone the link was sent to): nothing lands
on either account. Microsoft's token endpoint is the stand-in of the
Microsoft tests; the mailbox store, the connector rows, the roles and the
permission checks are real.
"""

from __future__ import annotations

from types import SimpleNamespace
from urllib.parse import parse_qs, urlsplit

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI
from sqlalchemy import select

from app.app_handoff import codes as handoff_codes, routes as handoff_routes
from app.connectors.email_imap.microsoft import routes as microsoft_routes
from app.connectors.email_imap.microsoft.mailbox_store import MicrosoftMailboxStore
from app.connectors.email_imap.microsoft.settings import MicrosoftMailSettings
from app.db import SearchSourceConnector, SearchSourceConnectorType, get_async_session
from app.rate_limiter import limiter
from app.users import get_auth_context, require_session_context
from tests.unit.connectors.email_imap.microsoft.fake_microsoft_login import (
    CLIENT_ID,
    CLIENT_SECRET,
    MAILBOX,
    FakeMicrosoftLogin,
    s256,
)

pytestmark = pytest.mark.integration

SECRET = "integration-test-secret-key-0123456789"
CALLBACK = "/api/v1/auth/microsoft/mail/connector/callback"
SETTINGS = MicrosoftMailSettings(
    client_id=CLIENT_ID,
    client_secret=CLIENT_SECRET,
    redirect_uri=f"http://test{CALLBACK}",
    frontend_url="http://test",
    secret_key=SECRET,
)
APP = "soloclef://connectors/done"
VERIFIER = "integration-verifier-" + "v" * 30
COMPLETE = "/api/v1/app/handoff/complete"


@pytest_asyncio.fixture
async def microsoft_api(db_session, monkeypatch):
    """The hand-off and Microsoft routes; ``browser()`` is a new browser (its own cookies)."""
    monkeypatch.setattr(
        handoff_codes,
        "_codes",
        handoff_codes.MemoryStore(handoff_codes.CODE_LIFETIME_SECONDS),
    )
    completions = handoff_codes.MemoryStore(handoff_codes.COMPLETION_LIFETIME_SECONDS)
    monkeypatch.setattr(handoff_codes, "_completions", completions)
    monkeypatch.setattr(limiter, "enabled", False)
    from app.config import config

    monkeypatch.setattr(config, "APP_HANDOFF_RETURN", APP, raising=False)
    login = FakeMicrosoftLogin()
    app = FastAPI()
    app.state.limiter = limiter
    for router in (handoff_routes.router, microsoft_routes.router):
        app.include_router(router, prefix="/api/v1")
    acting = SimpleNamespace(auth=None)

    async def session_override():
        yield db_session

    app.dependency_overrides[get_async_session] = session_override
    app.dependency_overrides[get_auth_context] = lambda: acting.auth
    app.dependency_overrides[require_session_context] = lambda: acting.auth
    app.dependency_overrides[microsoft_routes.get_microsoft_mail_settings] = lambda: (
        SETTINGS
    )
    app.dependency_overrides[microsoft_routes.get_token_transport] = lambda: (
        login.transport
    )
    app.dependency_overrides[microsoft_routes.get_mailbox_store] = lambda: (
        MicrosoftMailboxStore(db_session)
    )
    opened: list[httpx.AsyncClient] = []

    def browser() -> httpx.AsyncClient:
        client = httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app), base_url="http://test"
        )
        opened.append(client)
        return client

    def as_user(auth, client: httpx.AsyncClient) -> httpx.AsyncClient:
        """The app's request: its bearer token stands for ``auth``."""
        acting.auth = auth
        return client

    yield SimpleNamespace(
        browser=browser, as_user=as_user, login=login, completions=completions
    )
    for client in opened:
        await client.aclose()


async def _code(api, owner, app_client) -> str:
    res = await api.as_user(owner.auth, app_client).post(
        "/api/v1/app/handoff",
        json={
            "purpose": "microsoft_mail",
            "workspace_id": owner.workspace.id,
            "code_challenge": s256(VERIFIER),
        },
    )
    assert res.status_code == 200, res.text
    return res.json()["code"]


async def _consent(api, sheet: httpx.AsyncClient, code: str) -> dict:
    """``sheet`` opens the link, says yes at Microsoft, and comes back."""
    page = await sheet.get(f"/api/v1/app/handoff/{code}")
    assert page.status_code == 303, page.text
    query = {
        k: v[0] for k, v in parse_qs(urlsplit(page.headers["location"]).query).items()
    }
    api.login.codes = {"good-code": query["code_challenge"]}
    back = await sheet.get(
        CALLBACK, params={"code": "good-code", "state": query["state"]}
    )
    assert back.status_code == 303, back.text
    target = urlsplit(back.headers["location"])
    assert f"{target.scheme}://{target.netloc}{target.path}" == APP
    return {k: v[0] for k, v in parse_qs(target.query).items()}


async def _mailboxes(db_session, owner) -> list[str]:
    connector = await db_session.scalar(
        select(SearchSourceConnector)
        .where(
            SearchSourceConnector.user_id == owner.user.id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
        )
        .execution_options(populate_existing=True)
    )
    if connector is None:
        return []
    return [a.get("username") for a in (connector.config or {}).get("accounts", [])]


async def test_the_apps_own_sheet_connects_the_mailbox_once_the_app_confirms(
    microsoft_api, db_session, make_owner
):
    owner = await make_owner()
    app_client = microsoft_api.browser()  # the app's requests (bearer)
    sheet = microsoft_api.browser()  # the browser sheet the app opens
    code = await _code(microsoft_api, owner, app_client)

    back = await _consent(microsoft_api, sheet, code)
    assert back["purpose"] == "microsoft_mail" and back["status"] == "confirm"
    assert await _mailboxes(db_session, owner) == []

    done = await microsoft_api.as_user(owner.auth, app_client).post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert done.status_code == 200, done.text
    assert done.json() == {"purpose": "microsoft_mail", "status": "connected"}
    assert await _mailboxes(db_session, owner) == [MAILBOX]


async def test_a_code_opened_by_a_second_browser_links_no_mailbox_to_its_maker(
    microsoft_api, db_session, make_owner
):
    maker = await make_owner()
    someone = await make_owner()
    makers_app = microsoft_api.browser()
    code = await _code(microsoft_api, maker, makers_app)

    # The link reaches someone else's browser (no cookie, no token of the
    # maker's), which goes through Microsoft's consent with their mailbox.
    their_browser = microsoft_api.browser()
    back = await _consent(microsoft_api, their_browser, code)
    assert back["status"] == "confirm"
    assert await _mailboxes(db_session, maker) == []
    assert await _mailboxes(db_session, someone) == []

    # What they hold is spendable only by the maker's app, with its verifier:
    # their own app (another account) is refused, even holding the verifier.
    theirs = await microsoft_api.as_user(someone.auth, microsoft_api.browser()).post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert theirs.status_code == 403
    assert theirs.json()["detail"]["code"] == "HANDOFF_WRONG_ACCOUNT"
    # Spent by that try: had the completion reached the maker, it would be dead now.
    late = await microsoft_api.as_user(maker.auth, makers_app).post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert late.status_code == 410
    assert await _mailboxes(db_session, maker) == []
    assert await _mailboxes(db_session, someone) == []


async def test_a_wrong_verifier_saves_nothing_and_spends_the_completion(
    microsoft_api, db_session, make_owner
):
    owner = await make_owner()
    app_client = microsoft_api.browser()
    code = await _code(microsoft_api, owner, app_client)
    back = await _consent(microsoft_api, microsoft_api.browser(), code)

    wrong = await microsoft_api.as_user(owner.auth, app_client).post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": "w" * 43}
    )
    assert wrong.status_code == 403
    assert wrong.json()["detail"]["code"] == "HANDOFF_WRONG_VERIFIER"
    right = await microsoft_api.as_user(owner.auth, app_client).post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert right.status_code == 410
    assert await _mailboxes(db_session, owner) == []


async def test_without_the_apps_challenge_there_is_no_microsoft_code(
    microsoft_api, make_owner
):
    owner = await make_owner()
    res = await microsoft_api.as_user(owner.auth, microsoft_api.browser()).post(
        "/api/v1/app/handoff",
        json={"purpose": "microsoft_mail", "workspace_id": owner.workspace.id},
    )
    assert res.status_code == 400
    assert res.json()["detail"]["code"] == "HANDOFF_CHALLENGE_REQUIRED"
