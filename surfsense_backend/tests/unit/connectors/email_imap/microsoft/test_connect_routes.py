"""Connecting a Microsoft mailbox through the HTTP door: consent, callback, saved.

The app's real routes on a bare FastAPI app; Microsoft's token endpoint is a
mock transport, and the mailbox store is an in-memory stand-in for the
connector row. Workspace permission checks are covered elsewhere and are
waved through here.
"""

from __future__ import annotations

from types import SimpleNamespace
from urllib.parse import parse_qs, urlsplit
from uuid import uuid4

import pytest
from fastapi import FastAPI, HTTPException
from starlette.testclient import TestClient

from app.auth.context import AuthContext
from app.connectors.email_imap.microsoft import routes
from app.connectors.email_imap.microsoft.account_record import (
    new_mailbox_record,
    stored_refresh_token,
)
from app.connectors.email_imap.microsoft.connect_attempt import COOKIE_NAME
from app.connectors.email_imap.microsoft.settings import (
    IMAP_SCOPE,
    MicrosoftMailSettings,
)
from app.db import get_async_session
from app.users import get_auth_context, require_session_context
from tests.unit.connectors.email_imap.microsoft.fake_microsoft_login import (
    CLIENT_ID,
    CLIENT_SECRET,
    MAILBOX,
    FakeMicrosoftLogin,
    SignedInAccount,
    id_token_for,
)

pytestmark = pytest.mark.unit

SECRET = "test-secret-key-that-is-32-bytes-or-more"
FRONTEND = "https://notes.example"
CALLBACK = "/api/v1/auth/microsoft/mail/connector/callback"
SETTINGS = MicrosoftMailSettings(
    client_id=CLIENT_ID,
    client_secret=CLIENT_SECRET,
    redirect_uri=f"https://notes.example{CALLBACK}",
    frontend_url=FRONTEND,
    secret_key=SECRET,
)
USER = SimpleNamespace(id=uuid4(), is_superuser=False, email="operator@example.org")


class MemoryStore:
    """The caller's mailboxes, as ``config["accounts"]`` would hold them."""

    def __init__(self) -> None:
        self.accounts: dict[str, list[dict]] = {}

    async def address_of(self, user_id, account_id):
        for account in self.accounts.get(str(user_id), []):
            if account["id"] == account_id:
                return account["username"]
        return None

    async def save(self, user_id, mailbox, grant, *, secret_key):
        from app.connectors.email_imap.microsoft.account_record import (
            is_same_mailbox,
            reconnected,
        )

        mine = self.accounts.setdefault(str(user_id), [])
        for i, account in enumerate(mine):
            if is_same_mailbox(account, mailbox):
                mine[i] = reconnected(account, mailbox, grant, secret_key=secret_key)
                return mine[i]
        record = new_mailbox_record(mailbox, grant, secret_key=secret_key)
        mine.append(record)
        return record


@pytest.fixture
def login():
    return FakeMicrosoftLogin()


@pytest.fixture
def store():
    return MemoryStore()


@pytest.fixture
def permitted(monkeypatch):
    calls = []

    async def allow(_session, _auth, workspace_id, *args, **kwargs):
        calls.append(workspace_id)

    monkeypatch.setattr(routes, "check_permission", allow)
    monkeypatch.setattr(routes, "check_workspace_access", allow)
    return calls


def _client(login, store, settings=SETTINGS) -> TestClient:
    app = FastAPI()
    app.include_router(routes.router, prefix="/api/v1")

    async def session():
        yield None

    async def auth() -> AuthContext:
        return AuthContext.session(USER)

    app.dependency_overrides[get_async_session] = session
    app.dependency_overrides[get_auth_context] = auth
    app.dependency_overrides[require_session_context] = auth
    app.dependency_overrides[routes.get_microsoft_mail_settings] = lambda: settings
    app.dependency_overrides[routes.get_token_transport] = lambda: login.transport
    app.dependency_overrides[routes.get_mailbox_store] = lambda: store
    return TestClient(app, base_url="https://notes.example", follow_redirects=False)


def _start(client, **body):
    res = client.post(
        "/api/v1/workspaces/7/connectors/email-imap/microsoft/connect", json=body
    )
    assert res.status_code == 200, res.text
    consent = urlsplit(res.json()["auth_url"])
    return res, {k: v[0] for k, v in parse_qs(consent.query).items()}, consent


def _finish(client, **params):
    return client.get(CALLBACK, params=params)


def _landing(res):
    assert res.status_code in (302, 303)
    target = urlsplit(res.headers["location"])
    return target, {k: v[0] for k, v in parse_qs(target.query).items()}


def test_availability_says_whether_the_server_can_connect(login, store, permitted):
    on = _client(login, store).get(
        "/api/v1/workspaces/7/connectors/email-imap/microsoft"
    )
    off = _client(login, store, MicrosoftMailSettings()).get(
        "/api/v1/workspaces/7/connectors/email-imap/microsoft"
    )
    assert on.json() == {"configured": True}
    assert off.json() == {"configured": False}


def test_connect_sends_the_browser_to_microsoft_with_pkce_and_a_bound_cookie(
    login, store, permitted
):
    client = _client(login, store)
    res, query, consent = _start(
        client, return_path="/dashboard/7/workspace-settings/connectors"
    )

    assert consent.netloc == "login.microsoftonline.com"
    assert consent.path == "/common/oauth2/v2.0/authorize"
    assert query["client_id"] == CLIENT_ID
    assert query["response_type"] == "code"
    assert query["redirect_uri"] == SETTINGS.redirect_uri
    assert query["code_challenge_method"] == "S256"
    assert len(query["code_challenge"]) == 43
    assert IMAP_SCOPE in query["scope"].split()
    assert "offline_access" in query["scope"].split()
    assert query["prompt"] == "select_account"
    assert "client_secret" not in query
    cookie = res.headers["set-cookie"]
    assert cookie.startswith(f"{COOKIE_NAME}=")
    assert "HttpOnly" in cookie
    assert f"Path={CALLBACK}" in cookie
    assert "Secure" in cookie
    assert permitted == [7]


def test_the_whole_round_trip_saves_an_encrypted_mailbox_and_returns_to_the_page(
    login, store, permitted
):
    client = _client(login, store)
    _res, query, _ = _start(
        client, return_path="/dashboard/7/workspace-settings/connectors"
    )
    # Microsoft accepts the code only with the verifier behind this challenge.
    login.codes = {"good-code": query["code_challenge"]}

    target, params = _landing(_finish(client, code="good-code", state=query["state"]))

    assert f"{target.scheme}://{target.netloc}" == FRONTEND
    assert target.path == "/dashboard/7/workspace-settings/connectors"
    assert params == {"microsoft_mail": "connected"}
    [account] = store.accounts[str(USER.id)]
    assert account["username"] == MAILBOX
    assert account["auth"] == "microsoft"
    assert stored_refresh_token(account, secret_key=SECRET) == "refresh-2"
    assert "access-1" not in str(account)


def test_connecting_the_same_mailbox_again_reconnects_it_instead_of_adding_one(
    login, store, permitted
):
    client = _client(login, store)
    for code in ("first", "second"):
        _res, query, _ = _start(client)
        login.codes = {code: query["code_challenge"]}
        _target, params = _landing(_finish(client, code=code, state=query["state"]))
        assert params["microsoft_mail"] == "connected"
    assert len(store.accounts[str(USER.id)]) == 1


def test_reconnect_suggests_the_mailbox_it_is_for(login, store, permitted):
    client = _client(login, store)
    _res, query, _ = _start(client)
    login.codes = {"good-code": query["code_challenge"]}
    _finish(client, code="good-code", state=query["state"])
    [account] = store.accounts[str(USER.id)]

    _res, query, _ = _start(client, account_id=account["id"])
    assert query["login_hint"] == MAILBOX


def test_a_callback_from_another_browser_is_refused(login, store, permitted):
    client = _client(login, store)
    _res, query, _ = _start(client)
    client.cookies.clear()

    target, params = _landing(_finish(client, code="good-code", state=query["state"]))

    assert params == {"microsoft_mail": "expired"}
    assert target.path == "/dashboard/7/workspace-settings/connectors"
    assert store.accounts == {}
    assert login.requests == []


def test_a_state_that_is_not_this_browsers_is_refused(login, store, permitted):
    client = _client(login, store)
    _start(client)
    _target, params = _landing(_finish(client, code="good-code", state="7.forged"))
    assert params == {"microsoft_mail": "expired"}
    assert login.requests == []


@pytest.mark.parametrize(
    ("error", "description", "outcome"),
    [
        ("access_denied", "AADSTS65004: User declined to consent", "denied"),
        ("consent_required", "AADSTS65001: needs consent", "admin_approval"),
        ("access_denied", "AADSTS90094: admin approval required", "admin_approval"),
        ("server_error", "AADSTS50000", "failed"),
    ],
)
def test_microsofts_refusals_land_back_on_the_page_with_a_reason(
    login, store, permitted, error, description, outcome
):
    client = _client(login, store)
    _res, query, _ = _start(client)
    _target, params = _landing(
        _finish(
            client, error=error, error_description=description, state=query["state"]
        )
    )
    assert params == {"microsoft_mail": outcome}
    assert store.accounts == {}


@pytest.mark.parametrize(
    ("change", "outcome"),
    [
        ({"granted_scope": "openid email"}, "scope_missing"),
        ({"issue_refresh_token": False}, "no_refresh_token"),
        ({"id_token": id_token_for(address="not-an-address")}, "no_address"),
        ({"id_token": id_token_for(aud="someone-elses-app")}, "no_address"),
    ],
)
def test_a_grant_that_cannot_read_mail_is_not_saved(
    login, store, permitted, change, outcome
):
    for name, value in change.items():
        setattr(login, name, value)
    client = _client(login, store)
    _res, query, _ = _start(client)
    login.codes = {"good-code": query["code_challenge"]}

    _target, params = _landing(_finish(client, code="good-code", state=query["state"]))

    assert params == {"microsoft_mail": outcome}
    assert store.accounts == {}


def test_without_settings_connect_is_unavailable(login, store, permitted):
    res = _client(login, store, MicrosoftMailSettings()).post(
        "/api/v1/workspaces/7/connectors/email-imap/microsoft/connect", json={}
    )
    assert res.status_code == 503
    assert res.json()["detail"]["code"] == "microsoft_mail_not_configured"


def test_without_permission_to_add_connectors_nothing_starts(login, store, monkeypatch):
    async def refuse(*_args, **_kwargs):
        raise HTTPException(status_code=403, detail="nope")

    monkeypatch.setattr(routes, "check_permission", refuse)
    res = _client(login, store).post(
        "/api/v1/workspaces/7/connectors/email-imap/microsoft/connect", json={}
    )
    assert res.status_code == 403
    assert "set-cookie" not in res.headers


def test_an_offsite_return_path_is_ignored(login, store, permitted):
    client = _client(login, store)
    _res, query, _ = _start(client, return_path="//evil.example/steal")
    login.codes = {"good-code": query["code_challenge"]}
    target, _params = _landing(_finish(client, code="good-code", state=query["state"]))
    assert f"{target.scheme}://{target.netloc}" == FRONTEND
    assert target.path == "/dashboard/7/workspace-settings/connectors"


def test_a_failed_save_still_lands_on_the_page_with_a_reason(login, store, permitted):
    async def broken(*_args, **_kwargs):
        raise RuntimeError("database went away")

    store.save = broken
    client = _client(login, store)
    _res, query, _ = _start(client)
    login.codes = {"good-code": query["code_challenge"]}

    _target, params = _landing(_finish(client, code="good-code", state=query["state"]))

    assert params == {"microsoft_mail": "failed"}


@pytest.mark.parametrize("state", ["².x", "١٢.x", "7².x"])
def test_a_state_with_unicode_digits_lands_on_the_dashboard(login, store, state):
    # "²".isdigit() is True but int("²") fails: such a state names no workspace.
    res = _finish(_client(login, store), code="c", state=state)
    target, params = _landing(res)
    assert target.path == "/dashboard"
    assert params == {"microsoft_mail": "expired"}


def test_a_non_ascii_state_on_a_browser_mid_connect_is_refused(login, store, permitted):
    client = _client(login, store)
    _start(client)
    target, params = _landing(_finish(client, code="good-code", state="7.é"))
    assert params == {"microsoft_mail": "expired"}
    assert target.path == "/dashboard/7/workspace-settings/connectors"
    assert login.requests == []


def test_without_settings_a_unicode_state_still_lands(login, store):
    res = _finish(_client(login, store, MicrosoftMailSettings()), state="².x")
    assert res.status_code in (302, 303)


def test_consent_asks_for_the_profile_scope(login, store, permitted):
    # Without it Microsoft leaves preferred_username and oid out of the id_token.
    _res, query, _ = _start(_client(login, store))
    assert {"openid", "email", "profile"} <= set(query["scope"].split())


def test_an_account_without_an_email_claim_connects_by_its_sign_in_name(
    login, store, permitted
):
    login.account = SignedInAccount(email=None, preferred_username="user@corp.example")
    client = _client(login, store)
    _res, query, _ = _start(client)
    login.codes = {"good-code": query["code_challenge"]}

    _target, params = _landing(_finish(client, code="good-code", state=query["state"]))

    assert params == {"microsoft_mail": "connected"}
    [account] = store.accounts[str(USER.id)]
    assert account["username"] == "user@corp.example"
    assert account["microsoft_subject"] == "tid-1:oid-1"
