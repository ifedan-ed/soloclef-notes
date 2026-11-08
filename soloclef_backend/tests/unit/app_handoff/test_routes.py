"""The app's browser hand-off through its HTTP doors, Drive and Microsoft mail.

The real routes on a bare FastAPI app: the app asks for a code with its
token, its browser sheet opens the code's page, and the flow that page starts
comes back to the app's own address. Microsoft's token endpoint is the mock
transport of the Microsoft tests; Google is not reached (its consent URL is
only built, and its callback is exercised on a refusal, which needs no
database). The whole Drive round trip, saved row included, is the
integration test tests/integration/connectors/google_drive/test_app_handoff.py;
Microsoft's, with a link opened by a second browser, is
tests/integration/app_handoff/test_microsoft_mail_handoff.py.

A Microsoft mailbox or a Google Drive is saved only when the app confirms
it (``POST /app/handoff/complete``) with the verifier behind the challenge it
asked for the code with, as the account that asked: the hand-off page takes
no credentials, so the browser that comes back may be anyone's. Google's
code exchange is faked here (``google_says_yes``) to reach that far.
"""

from __future__ import annotations

import time
from types import SimpleNamespace
from urllib.parse import parse_qs, urlsplit
from uuid import uuid4

import pytest
from fastapi import FastAPI, HTTPException
from starlette.testclient import TestClient

from app.app_handoff import routes as handoff_routes
from app.app_handoff.codes import (
    CODE_LIFETIME_SECONDS,
    COMPLETION_LIFETIME_SECONDS,
    MemoryHandoffCodes,
    MemoryStore as MemoryCodes,
    completion_store,
)
from app.auth.context import AuthContext
from app.config import config
from app.connectors.email_imap.microsoft import routes as microsoft_routes
from app.connectors.email_imap.microsoft.connect_attempt import COOKIE_NAME
from app.connectors.email_imap.microsoft.settings import MicrosoftMailSettings
from app.connectors.google_drive.connect import routes as drive_routes
from app.connectors.google_drive.connect.connect_attempt import (
    COOKIE_NAME as DRIVE_COOKIE,
)
from app.connectors.google_drive.connect.scopes import DRIVE_FILE_SCOPE
from app.db import get_async_session
from app.users import get_auth_context, require_session_context
from tests.unit.connectors.email_imap.microsoft.fake_microsoft_login import (
    CLIENT_ID,
    CLIENT_SECRET,
    MAILBOX,
    FakeMicrosoftLogin,
    s256,
)
from tests.unit.connectors.email_imap.microsoft.test_connect_routes import (
    MemoryStore,
)

pytestmark = pytest.mark.unit

SECRET = "test-secret-key-that-is-32-bytes-or-more"
MS_CALLBACK = "/api/v1/auth/microsoft/mail/connector/callback"
MICROSOFT = MicrosoftMailSettings(
    client_id=CLIENT_ID,
    client_secret=CLIENT_SECRET,
    redirect_uri=f"https://notes.example{MS_CALLBACK}",
    frontend_url="https://notes.example",
    secret_key=SECRET,
)
USER = SimpleNamespace(id=uuid4(), is_superuser=False, email="probe@example.org")
OTHER = SimpleNamespace(id=uuid4(), is_superuser=False, email="other@example.org")
APP = "soloclef://connectors/done"
# The app's own PKCE pair for a Microsoft hand-off (RFC 7636's example verifier).
VERIFIER = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
CHALLENGE = s256(VERIFIER)
COMPLETE = "/api/v1/app/handoff/complete"
DRIVE_CALLBACK = "/api/v1/auth/google/drive/connector/callback"


@pytest.fixture
def drive_configured(monkeypatch):
    monkeypatch.setattr(
        config, "GOOGLE_OAUTH_CLIENT_ID", "1234-unit.apps.googleusercontent.com"
    )
    monkeypatch.setattr(config, "GOOGLE_OAUTH_CLIENT_SECRET", "unit-client-secret")
    monkeypatch.setattr(
        config,
        "GOOGLE_DRIVE_REDIRECT_URI",
        "https://notes.example/api/v1/auth/google/drive/connector/callback",
    )
    monkeypatch.setattr(config, "GOOGLE_PICKER_API_KEY", "unit-picker-key")
    monkeypatch.setattr(config, "SECRET_KEY", SECRET)
    monkeypatch.setattr(config, "APP_HANDOFF_RETURN", APP, raising=False)


@pytest.fixture
def permitted(monkeypatch):
    """Workspace permissions are covered elsewhere: waved through, and counted."""
    calls: list[int] = []

    async def allow(_session, _auth, workspace_id, *args, **kwargs):
        calls.append(workspace_id)

    for module in (handoff_routes, drive_routes, microsoft_routes):
        monkeypatch.setattr(module, "check_permission", allow)
    return calls


@pytest.fixture
def world(drive_configured):
    login = FakeMicrosoftLogin()
    store = MemoryStore()
    codes = MemoryHandoffCodes()
    completions = MemoryCodes(COMPLETION_LIFETIME_SECONDS)
    settings = {"microsoft": MICROSOFT}
    # Who the bearer token belongs to (the app's requests; the pages take none).
    acting = {"user": USER}
    app = FastAPI()
    app.include_router(handoff_routes.router, prefix="/api/v1")
    app.include_router(drive_routes.router, prefix="/api/v1")
    app.include_router(microsoft_routes.router, prefix="/api/v1")

    async def session():
        yield None

    async def auth() -> AuthContext:
        return AuthContext.session(acting["user"])

    app.dependency_overrides[get_async_session] = session
    app.dependency_overrides[get_auth_context] = auth
    app.dependency_overrides[require_session_context] = auth
    app.dependency_overrides[handoff_routes.get_handoff_codes] = lambda: codes
    app.dependency_overrides[completion_store] = lambda: completions
    app.dependency_overrides[microsoft_routes.get_microsoft_mail_settings] = lambda: (
        settings["microsoft"]
    )
    app.dependency_overrides[microsoft_routes.get_token_transport] = lambda: (
        login.transport
    )
    app.dependency_overrides[microsoft_routes.get_mailbox_store] = lambda: store
    client = TestClient(app, base_url="https://notes.example", follow_redirects=False)
    return SimpleNamespace(
        app=app,
        client=client,
        login=login,
        store=store,
        codes=codes,
        completions=completions,
        settings=settings,
        acting=acting,
    )


def _code(client, purpose: str, **extra) -> str:
    if purpose in ("microsoft_mail", "drive_connect"):
        extra.setdefault("code_challenge", CHALLENGE)
    res = client.post(
        "/api/v1/app/handoff", json={"purpose": purpose, "workspace_id": 7, **extra}
    )
    assert res.status_code == 200, res.text
    assert res.json()["expires_in"] == CODE_LIFETIME_SECONDS
    return res.json()["code"]


def _where(res):
    assert res.status_code == 303, res.text
    target = urlsplit(res.headers["location"])
    return target, {k: v[0] for k, v in parse_qs(target.query).items()}


def _app_return(res):
    target, params = _where(res)
    assert f"{target.scheme}://{target.netloc}{target.path}" == APP
    return params


@pytest.fixture
def google_says_yes(monkeypatch):
    """Google's code exchange, faked: the verifier each exchange was given, and every save."""
    seen: dict = {"verifiers": [], "saved": []}

    async def exchange(code, code_verifier):
        seen["verifiers"].append(code_verifier)
        return SimpleNamespace(
            refresh_token="1//refresh", granted_scopes=[DRIVE_FILE_SCOPE], scopes=None
        )

    async def email_of(_credentials):
        return "drive.owner@example.org"

    async def save(_session, *, user_id, workspace_id, grant, email):
        seen["saved"].append((str(user_id), workspace_id, grant, email))
        return SimpleNamespace(id=31)

    monkeypatch.setattr(drive_routes, "exchange_code", exchange)
    monkeypatch.setattr(drive_routes, "account_email", email_of)
    monkeypatch.setattr(
        drive_routes,
        "credentials_config",
        lambda _c: {"token": "ya29.access", "refresh_token": "1//refresh"},
    )
    monkeypatch.setattr(drive_routes, "save_connection", save)
    return seen


def test_a_code_is_given_only_after_the_websites_own_checks(world, permitted):
    code = _code(world.client, "drive_connect")
    assert permitted == [7]
    assert code not in str(world.codes._codes)


def test_without_permission_to_add_connectors_there_is_no_code(world, monkeypatch):
    async def refuse(*_args, **_kwargs):
        raise HTTPException(status_code=403, detail="nope")

    monkeypatch.setattr(handoff_routes, "check_permission", refuse)
    res = world.client.post(
        "/api/v1/app/handoff", json={"purpose": "drive_connect", "workspace_id": 7}
    )
    assert res.status_code == 403
    assert world.codes._codes == {}


def test_a_provider_the_server_lacks_is_refused_up_front(world, permitted, monkeypatch):
    monkeypatch.setattr(config, "GOOGLE_PICKER_API_KEY", None)
    drive = world.client.post(
        "/api/v1/app/handoff", json={"purpose": "drive_connect", "workspace_id": 7}
    )
    assert drive.status_code == 503
    assert drive.json()["detail"]["code"] == "google_drive_not_configured"
    world.settings["microsoft"] = MicrosoftMailSettings()
    for extra in ({"code_challenge": CHALLENGE}, {}):
        # With or without the app's challenge: an older app build hears it too.
        mail = world.client.post(
            "/api/v1/app/handoff",
            json={"purpose": "microsoft_mail", "workspace_id": 7, **extra},
        )
        assert mail.status_code == 503
        assert mail.json()["detail"]["code"] == "microsoft_mail_not_configured"


def test_an_unknown_purpose_is_refused(world, permitted):
    res = world.client.post(
        "/api/v1/app/handoff", json={"purpose": "anything", "workspace_id": 7}
    )
    assert res.status_code == 400
    assert res.json()["detail"]["code"] == "HANDOFF_PURPOSE_UNSUPPORTED"


def test_a_code_opens_once_then_sends_the_sheet_back_expired(world, permitted):
    code = _code(world.client, "drive_connect")
    first = world.client.get(f"/api/v1/app/handoff/{code}")
    target, _ = _where(first)
    assert target.netloc == "accounts.google.com"
    assert first.headers["cache-control"] == "no-store"
    assert first.headers["referrer-policy"] == "no-referrer"
    again = world.client.get(f"/api/v1/app/handoff/{code}")
    assert _app_return(again) == {"status": "expired"}
    assert again.headers["referrer-policy"] == "no-referrer"
    assert _app_return(world.client.get("/api/v1/app/handoff/never-issued")) == {
        "status": "expired"
    }


def test_drive_starts_googles_consent_with_the_attempt_in_the_sheets_cookie(
    world, permitted
):
    code = _code(world.client, "drive_connect")
    res = world.client.get(f"/api/v1/app/handoff/{code}")
    target, query = _where(res)
    assert target.path == "/o/oauth2/auth"
    assert query["access_type"] == "offline"
    assert query["code_challenge_method"] == "S256"
    # The state names the workspace and nothing else; who is connecting and
    # the PKCE verifier are in the cookie, never in a URL.
    assert query["state"].startswith("7.")
    assert str(USER.id) not in query["state"]
    cookie = res.headers["set-cookie"]
    assert cookie.startswith(f"{DRIVE_COOKIE}=")
    assert "HttpOnly" in cookie
    assert f"Path={DRIVE_CALLBACK}" in cookie


def test_a_drive_consent_the_app_started_comes_back_to_the_app(world, permitted):
    code = _code(world.client, "drive_connect")
    _target, query = _where(world.client.get(f"/api/v1/app/handoff/{code}"))
    # Cancel on Google's page: refused before any database work.
    res = world.client.get(
        f"/api/v1{drive_routes.CALLBACK_PATH}",
        params={"error": "access_denied", "state": query["state"]},
    )
    assert _app_return(res) == {
        "purpose": "drive_connect",
        "status": "google_drive_oauth_denied",
    }


def test_the_websites_own_drive_consent_still_returns_to_the_website(
    world, permitted, monkeypatch
):
    monkeypatch.setattr(config, "NEXT_FRONTEND_URL", "https://notes.example")
    started = world.client.post(
        "/api/v1/workspaces/7/google-drive/connect",
        json={"return_path": "/dashboard/7/workspace-settings/connectors"},
    )
    query = {
        k: v[0] for k, v in parse_qs(urlsplit(started.json()["auth_url"]).query).items()
    }
    assert started.headers["set-cookie"].startswith(f"{DRIVE_COOKIE}=")
    res = world.client.get(
        f"/api/v1{drive_routes.CALLBACK_PATH}",
        params={"error": "access_denied", "state": query["state"]},
    )
    assert res.status_code in (302, 303, 307)
    assert (
        res.headers["location"]
        == "https://notes.example/dashboard/7/workspace-settings/connectors?google_drive=google_drive_oauth_denied"
    )


def test_microsoft_mail_sets_the_attempt_cookie_in_the_sheet_and_goes_to_microsoft(
    world, permitted
):
    code = _code(world.client, "microsoft_mail")
    res = world.client.get(f"/api/v1/app/handoff/{code}")
    target, query = _where(res)
    assert target.netloc == "login.microsoftonline.com"
    assert query["code_challenge_method"] == "S256"
    cookie = res.headers["set-cookie"]
    assert cookie.startswith(f"{COOKIE_NAME}=")
    assert "HttpOnly" in cookie
    assert f"Path={MS_CALLBACK}" in cookie
    assert res.headers["referrer-policy"] == "no-referrer"


def _through_microsoft(world, code: str, client=None):
    """The sheet opens the code's page, says yes at Microsoft, and comes back."""
    browser = client or world.client
    _target, query = _where(browser.get(f"/api/v1/app/handoff/{code}"))
    world.login.codes = {"good-code": query["code_challenge"]}
    return browser.get(
        MS_CALLBACK, params={"code": "good-code", "state": query["state"]}
    )


def test_microsoft_mail_round_trip_holds_the_mailbox_until_the_app_confirms(
    world, permitted
):
    code = _code(world.client, "microsoft_mail")
    res = _through_microsoft(world, code)
    back = _app_return(res)
    assert back["purpose"] == "microsoft_mail" and back["status"] == "confirm"
    assert len(back["completion"]) >= 40
    assert world.store.accounts == {}, "nothing is saved before the app confirms"
    assert COOKIE_NAME in res.headers.get("set-cookie", "")  # cleared
    assert res.headers["referrer-policy"] == "no-referrer"
    # The completion is stored hashed, its mailbox encrypted.
    assert back["completion"] not in str(world.completions._items)
    assert "access-1" not in str(world.completions._items)

    done = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert done.status_code == 200, done.text
    assert done.json() == {"purpose": "microsoft_mail", "status": "connected"}
    assert done.headers["cache-control"] == "no-store"
    [account] = world.store.accounts[str(USER.id)]
    assert account["username"] == MAILBOX
    again = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert again.status_code == 410
    assert len(world.store.accounts[str(USER.id)]) == 1


def test_microsoft_mail_needs_the_apps_challenge(world, permitted):
    # An app build from before completions asks without one: it is told to update.
    res = world.client.post(
        "/api/v1/app/handoff", json={"purpose": "microsoft_mail", "workspace_id": 7}
    )
    assert res.status_code == 400
    assert res.json()["detail"]["code"] == "HANDOFF_CHALLENGE_REQUIRED"
    assert world.codes._codes == {}
    for bad in ("short", "x" * 44, "e" * 42 + "!"):
        res = world.client.post(
            "/api/v1/app/handoff",
            json={
                "purpose": "microsoft_mail",
                "workspace_id": 7,
                "code_challenge": bad,
            },
        )
        assert res.status_code == 422


def test_a_link_opened_in_another_browser_links_nothing_to_its_maker(world, permitted):
    # The account's owner asks for a code and sends the link to someone else,
    # whose browser (no cookie, no token) goes through Microsoft with their mailbox.
    code = _code(world.client, "microsoft_mail")
    someone = TestClient(
        world.app, base_url="https://notes.example", follow_redirects=False
    )
    back = _app_return(_through_microsoft(world, code, client=someone))
    assert back["status"] == "confirm"
    assert world.store.accounts == {}

    # Their own app (another account) cannot spend it, even with the verifier.
    world.acting["user"] = OTHER
    theirs = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert theirs.status_code == 403
    assert theirs.json()["detail"]["code"] == "HANDOFF_WRONG_ACCOUNT"
    # And the link's maker never saw the completion; it is spent all the same.
    world.acting["user"] = USER
    late = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert late.status_code == 410
    assert world.store.accounts == {}


def test_a_completion_needs_the_verifier_behind_the_challenge(world, permitted):
    code = _code(world.client, "microsoft_mail")
    back = _app_return(_through_microsoft(world, code))
    wrong = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": "v" * 43}
    )
    assert wrong.status_code == 403
    assert wrong.json()["detail"]["code"] == "HANDOFF_WRONG_VERIFIER"
    # Spent by the wrong try: the right verifier comes too late.
    right = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert right.status_code == 410
    assert world.store.accounts == {}


def test_a_guessed_or_old_completion_saves_nothing(world, permitted):
    guessed = world.client.post(
        COMPLETE, json={"completion": "g" * 43, "code_verifier": VERIFIER}
    )
    assert guessed.status_code == 410
    code = _code(world.client, "microsoft_mail")
    back = _app_return(_through_microsoft(world, code))
    # Five minutes and a second later.
    world.completions._clock = lambda: time.time() + COMPLETION_LIFETIME_SECONDS + 1
    old = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert old.status_code == 410
    assert world.store.accounts == {}


def test_a_completion_without_permission_in_the_workspace_saves_nothing(
    world, permitted, monkeypatch
):
    code = _code(world.client, "microsoft_mail")
    back = _app_return(_through_microsoft(world, code))

    async def refuse(*_args, **_kwargs):
        raise HTTPException(status_code=403, detail="no longer a member")

    monkeypatch.setattr(handoff_routes, "check_permission", refuse)
    res = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert res.status_code == 403
    assert world.store.accounts == {}


def test_the_callback_of_an_app_attempt_never_saves_without_its_challenge(
    world, permitted
):
    # A cookie for an app attempt with no challenge (none is ever made): refused.
    from app.connectors.email_imap.microsoft.connect_attempt import ConnectAttempt
    from app.connectors.email_imap.microsoft.consent import consent_url

    attempt = ConnectAttempt.start(user_id=USER.id, workspace_id=7, app=True)
    world.client.cookies.set(COOKIE_NAME, attempt.seal(SECRET))
    query = {
        k: v[0]
        for k, v in parse_qs(urlsplit(consent_url(MICROSOFT, attempt)).query).items()
    }
    world.login.codes = {"good-code": query["code_challenge"]}
    res = world.client.get(
        MS_CALLBACK, params={"code": "good-code", "state": query["state"]}
    )
    assert _app_return(res) == {"purpose": "microsoft_mail", "status": "failed"}
    assert world.store.accounts == {}
    assert world.completions._items == {}


def test_microsoft_refusals_also_return_to_the_app_with_the_reason(world, permitted):
    code = _code(world.client, "microsoft_mail")
    _target, query = _where(world.client.get(f"/api/v1/app/handoff/{code}"))
    res = world.client.get(
        MS_CALLBACK,
        params={
            "error": "access_denied",
            "error_description": "AADSTS65004",
            "state": query["state"],
        },
    )
    assert _app_return(res) == {"purpose": "microsoft_mail", "status": "denied"}
    assert world.store.accounts == {}


def test_microsoft_reconnect_preselects_the_mailbox(world, permitted):
    first = _code(world.client, "microsoft_mail")
    back = _app_return(_through_microsoft(world, first))
    world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    [account] = world.store.accounts[str(USER.id)]
    again = _code(world.client, "microsoft_mail", account_id=account["id"])
    _target, query = _where(world.client.get(f"/api/v1/app/handoff/{again}"))
    assert query["login_hint"] == MAILBOX


def test_the_websites_own_microsoft_connect_still_returns_to_the_website(
    world, permitted
):
    started = world.client.post(
        "/api/v1/workspaces/7/connectors/email-imap/microsoft/connect", json={}
    )
    query = {
        k: v[0] for k, v in parse_qs(urlsplit(started.json()["auth_url"]).query).items()
    }
    res = world.client.get(
        MS_CALLBACK, params={"error": "access_denied", "state": query["state"]}
    )
    target, params = _where(res)
    assert f"{target.scheme}://{target.netloc}" == "https://notes.example"
    assert params == {"microsoft_mail": "denied"}


def test_a_spent_microsoft_code_cannot_start_a_second_attempt(world, permitted):
    code = _code(world.client, "microsoft_mail")
    world.client.get(f"/api/v1/app/handoff/{code}")
    again = world.client.get(f"/api/v1/app/handoff/{code}")
    assert _app_return(again) == {"status": "expired"}
    assert "set-cookie" not in again.headers


def _through_google(world, code: str, client=None):
    """The sheet opens a Drive code's page, says yes at Google, and comes back."""
    browser = client or world.client
    _target, query = _where(browser.get(f"/api/v1/app/handoff/{code}"))
    res = browser.get(DRIVE_CALLBACK, params={"code": "good", "state": query["state"]})
    return query, res


def test_drive_needs_the_apps_challenge(world, permitted):
    # An app build from before Drive completions asks without one: told to update.
    res = world.client.post(
        "/api/v1/app/handoff", json={"purpose": "drive_connect", "workspace_id": 7}
    )
    assert res.status_code == 400
    assert res.json()["detail"]["code"] == "HANDOFF_CHALLENGE_REQUIRED"
    assert "Google Drive" in res.json()["detail"]["message"]
    assert world.codes._codes == {}


def test_drive_round_trip_holds_the_grant_until_the_app_confirms(
    world, permitted, google_says_yes
):
    code = _code(world.client, "drive_connect")
    query, res = _through_google(world, code)
    back = _app_return(res)
    assert back["purpose"] == "drive_connect" and back["status"] == "confirm"
    assert len(back["completion"]) >= 40
    assert google_says_yes["saved"] == [], "nothing is saved before the app confirms"
    # PKCE: Google got the challenge, the exchange the verifier behind it.
    [verifier] = google_says_yes["verifiers"]
    assert s256(verifier) == query["code_challenge"]
    assert DRIVE_COOKIE in res.headers.get("set-cookie", "")  # cleared
    # Held encrypted, under the completion's hash.
    assert "ya29.access" not in str(world.completions._items)
    assert back["completion"] not in str(world.completions._items)

    done = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert done.status_code == 200, done.text
    assert done.json() == {"purpose": "drive_connect", "status": "connected"}
    assert google_says_yes["saved"] == [
        (
            str(USER.id),
            7,
            {"token": "ya29.access", "refresh_token": "1//refresh"},
            "drive.owner@example.org",
        )
    ]
    again = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert again.status_code == 410
    assert len(google_says_yes["saved"]) == 1


def test_a_drive_link_opened_in_another_browser_links_nothing_to_its_maker(
    world, permitted, google_says_yes
):
    # The account's owner asks for a code and sends the link to someone else,
    # whose browser goes through Google with their own Drive.
    code = _code(world.client, "drive_connect")
    someone = TestClient(
        world.app, base_url="https://notes.example", follow_redirects=False
    )
    _query, res = _through_google(world, code, client=someone)
    back = _app_return(res)
    assert back["status"] == "confirm"
    assert google_says_yes["saved"] == []
    # Their own app (another account) cannot spend it, even with the verifier.
    world.acting["user"] = OTHER
    theirs = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": VERIFIER}
    )
    assert theirs.status_code == 403
    assert theirs.json()["detail"]["code"] == "HANDOFF_WRONG_ACCOUNT"
    assert google_says_yes["saved"] == []


def test_a_drive_completion_needs_the_verifier_behind_the_challenge(
    world, permitted, google_says_yes
):
    code = _code(world.client, "drive_connect")
    _query, res = _through_google(world, code)
    back = _app_return(res)
    wrong = world.client.post(
        COMPLETE, json={"completion": back["completion"], "code_verifier": "v" * 43}
    )
    assert wrong.status_code == 403
    assert wrong.json()["detail"]["code"] == "HANDOFF_WRONG_VERIFIER"
    assert google_says_yes["saved"] == []


def test_the_websites_drive_callback_saves_only_in_the_browser_that_started_it(
    world, permitted, google_says_yes, monkeypatch
):
    monkeypatch.setattr(config, "NEXT_FRONTEND_URL", "https://notes.example")
    started = world.client.post("/api/v1/workspaces/7/google-drive/connect", json={})
    query = {
        k: v[0] for k, v in parse_qs(urlsplit(started.json()["auth_url"]).query).items()
    }
    # Someone else's browser (no cookie), with the starter's link, says yes at
    # Google with their own Drive: nothing is saved anywhere.
    someone = TestClient(
        world.app, base_url="https://notes.example", follow_redirects=False
    )
    refused = someone.get(
        DRIVE_CALLBACK, params={"code": "good", "state": query["state"]}
    )
    assert refused.status_code in (302, 303, 307)
    assert refused.headers["location"] == (
        "https://notes.example/dashboard/7/connectors/callback"
        "?error=google_drive_connect_expired&connector=google-drive-connector"
    )
    assert google_says_yes["saved"] == []
    assert google_says_yes["verifiers"] == []
    # The browser that pressed Connect finishes it, once.
    done = world.client.get(
        DRIVE_CALLBACK, params={"code": "good", "state": query["state"]}
    )
    assert "success=true" in done.headers["location"]
    assert [saved[:2] for saved in google_says_yes["saved"]] == [(str(USER.id), 7)]
    replay = world.client.get(
        DRIVE_CALLBACK, params={"code": "good", "state": query["state"]}
    )
    assert "google_drive_connect_expired" in replay.headers["location"]
    assert len(google_says_yes["saved"]) == 1


def test_a_state_that_is_not_this_browsers_own_is_refused(
    world, permitted, google_says_yes
):
    world.client.post("/api/v1/workspaces/7/google-drive/connect", json={})
    forged = world.client.get(
        DRIVE_CALLBACK, params={"code": "good", "state": "7.forged"}
    )
    assert "google_drive_connect_expired" in forged.headers["location"]
    assert google_says_yes["saved"] == [] and google_says_yes["verifiers"] == []
