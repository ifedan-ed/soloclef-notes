"""Sign in through the provider, over HTTP, against real Postgres.

The provider is the external boundary and is faked (real RS256 keys, PKCE
checked); the routes, cookies, id_token checks and account rows are real.
"""

from __future__ import annotations

import uuid
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager
from urllib.parse import parse_qs, urlsplit

import httpx
import pytest
import pytest_asyncio
from fastapi import FastAPI, HTTPException, Request
from fastapi_users.exceptions import UserAlreadyExists
from fastapi_users.password import PasswordHelper
from httpx import ASGITransport
from sqlalchemy import func, select, text
from sqlalchemy.ext.asyncio import AsyncSession

import app.auth_oidc.web_session as web_session
import app.users as app_users
from app.auth_oidc.account_link import identity_links
from app.auth_oidc.discovery import ProviderDirectory
from app.auth_oidc.routes import build_oidc_router
from app.auth_oidc.settings import OidcSettings
from app.config import config
from app.db import (
    Prompt,
    User,
    Workspace,
    WorkspaceMembership,
    WorkspaceRole,
    get_async_session,
)
from app.prompts.system_defaults import SYSTEM_PROMPT_DEFAULTS
from app.users import UserManager
from tests.utils.fake_oidc_provider import (
    CLIENT_ID,
    CLIENT_SECRET,
    DISCOVERY_URL,
    REDIRECT_URI,
    FakeOidcProvider,
)

pytestmark = pytest.mark.integration

NOTES = "https://notes.test"
SECRET = "integration-test-secret-key-0123456789"
AUTOCREATE_DOMAINS = frozenset({"example.com"})

# Taken before any fixture swaps it out, for the tests that need sign-up's
# real side effects.
REAL_ON_AFTER_REGISTER = UserManager.on_after_register

# The table as upstream's fastapi-users created it on the live database.
OAUTH_ACCOUNT_DDL = """
CREATE TABLE IF NOT EXISTS oauth_account (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    oauth_name varchar(100) NOT NULL,
    access_token varchar(1024) NOT NULL,
    expires_at integer,
    refresh_token varchar(1024),
    account_id varchar(320) NOT NULL,
    account_email varchar(320) NOT NULL
)
"""


def _settings(**changes) -> OidcSettings:
    base = {
        "enabled": True,
        "discovery_url": DISCOVERY_URL,
        "client_id": CLIENT_ID,
        "client_secret": CLIENT_SECRET,
        "redirect_uri": REDIRECT_URI,
        "frontend_url": NOTES,
    }
    base.update(changes)
    return OidcSettings(**base)


@pytest.fixture
def provider() -> FakeOidcProvider:
    return FakeOidcProvider()


@pytest.fixture(autouse=True)
def _no_side_sessions(monkeypatch):
    # These open their own connections, which would wait on this test's
    # uncommitted rows; what they do is covered by the password sign-in tests.
    async def create_refresh_token(user_id):
        return f"refresh-for-{user_id}"

    async def nothing(self, *args, **kwargs):
        return None

    monkeypatch.setattr(web_session, "create_refresh_token", create_refresh_token)
    monkeypatch.setattr(UserManager, "on_after_login", nothing)
    monkeypatch.setattr(UserManager, "on_after_register", nothing)


@pytest_asyncio.fixture
async def oauth_table(db_session: AsyncSession) -> None:
    await db_session.execute(text(OAUTH_ACCOUNT_DDL))


@pytest.fixture
def sign_up_side_effects(monkeypatch, db_session):
    """The real ``on_after_register``, run inside this test's transaction.

    It opens its own session through ``async_session_maker``; pointing that at
    the test's session lets the workspace, roles, membership and prompts it
    writes see the uncommitted user, and roll back with everything else.
    """

    @asynccontextmanager
    async def this_transaction():
        yield db_session

    monkeypatch.setattr(app_users, "async_session_maker", this_transaction)
    monkeypatch.setattr(UserManager, "on_after_register", REAL_ON_AFTER_REGISTER)


class Attempts:
    """A stand-in for the per-IP limiter: allows ``allowed`` calls, then 429."""

    def __init__(self, allowed: int = 1000) -> None:
        self.allowed = allowed
        self.used = 0

    def __call__(self, request: Request) -> None:
        self.used += 1
        if self.used > self.allowed:
            raise HTTPException(status_code=429, detail="RATE_LIMIT_EXCEEDED")


def _client_for(
    db_session: AsyncSession,
    provider: FakeOidcProvider,
    settings: OidcSettings,
    attempts: Attempts,
) -> httpx.AsyncClient:
    app = FastAPI()
    app.include_router(
        build_oidc_router(
            settings,
            secret=SECRET,
            rate_limit=attempts,
            directory=ProviderDirectory(DISCOVERY_URL, transport=provider.transport),
        )
    )

    async def same_session() -> AsyncGenerator[AsyncSession, None]:
        yield db_session

    app.dependency_overrides[get_async_session] = same_session
    return httpx.AsyncClient(transport=ASGITransport(app=app), base_url=NOTES)


@pytest_asyncio.fixture
async def client(db_session, provider, oauth_table):
    async with _client_for(db_session, provider, _settings(), Attempts()) as http:
        yield http


@pytest_asyncio.fixture
async def person(db_session: AsyncSession) -> User:
    user = User(
        id=uuid.uuid4(),
        email=f"oidc-{uuid.uuid4().hex[:8]}@example.com",
        hashed_password=PasswordHelper().hash("a password nobody types"),
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    return user


async def _sign_in(client, provider, **identity) -> httpx.Response:
    start = await client.get("/auth/oidc/authorize-redirect")
    assert start.status_code == 302
    return await client.get(provider.approve(start.headers["location"], **identity))


def _refusal(response: httpx.Response) -> str | None:
    location = urlsplit(response.headers["location"])
    assert f"{location.scheme}://{location.netloc}{location.path}" == f"{NOTES}/login"
    return parse_qs(location.query)["error"][0]


async def _links(db_session, user_id) -> list:
    rows = await db_session.execute(
        select(identity_links).where(identity_links.c.user_id == user_id)
    )
    return list(rows.mappings())


async def test_start_sends_the_browser_to_the_provider(client, provider):
    response = await client.get("/auth/oidc/authorize-redirect")

    assert response.status_code == 302
    location = response.headers["location"]
    assert location.startswith("https://sso.test/application/o/authorize/?")
    query = parse_qs(urlsplit(location).query)
    assert query["client_id"] == [CLIENT_ID]
    assert query["redirect_uri"] == [REDIRECT_URI]
    assert query["code_challenge_method"] == ["S256"]
    cookie = response.headers["set-cookie"]
    assert "notes_oidc_login=" in cookie
    assert "HttpOnly" in cookie
    assert "Path=/auth/oidc" in cookie


async def test_first_sign_in_links_the_account_with_that_email(
    client, provider, person, db_session
):
    response = await _sign_in(
        client, provider, sub="authentik-42", email=person.email.upper(), name="Dan"
    )

    assert response.status_code == 303
    assert response.headers["location"] == f"{NOTES}/dashboard"
    assert client.cookies.get(config.SESSION_COOKIE_NAME)
    assert client.cookies.get(config.REFRESH_COOKIE_NAME) == f"refresh-for-{person.id}"
    links = await _links(db_session, person.id)
    assert [(row["oauth_name"], row["account_id"]) for row in links] == [
        ("authentik", "authentik-42")
    ]
    # Authentik's ~2,300-character access token is never stored.
    assert links[0]["access_token"] == ""
    assert links[0]["refresh_token"] is None
    await db_session.refresh(person)
    assert person.display_name == "Dan"


async def test_lands_on_the_page_the_person_was_sent_away_from(
    client, provider, person
):
    start = await client.get("/auth/oidc/authorize-redirect?next=/notebooks/9")
    callback = provider.approve(
        start.headers["location"], sub="sub-n", email=person.email
    )

    response = await client.get(callback)

    assert response.headers["location"] == f"{NOTES}/notebooks/9"


async def test_never_lands_on_another_site(client, provider, person):
    start = await client.get("/auth/oidc/authorize-redirect?next=//evil.example/steal")
    callback = provider.approve(
        start.headers["location"], sub="sub-e", email=person.email
    )

    response = await client.get(callback)

    assert response.headers["location"] == f"{NOTES}/dashboard"


async def test_next_sign_in_finds_the_account_by_subject_even_after_an_email_change(
    client, provider, person, db_session
):
    await _sign_in(client, provider, sub="authentik-42", email=person.email)
    client.cookies.clear()

    again = await _sign_in(
        client, provider, sub="authentik-42", email="renamed@example.com"
    )

    assert again.status_code == 303
    assert again.headers["location"] == f"{NOTES}/dashboard"
    links = await _links(db_session, person.id)
    assert len(links) == 1
    assert links[0]["account_email"] == "renamed@example.com"


async def test_an_address_with_no_notes_account_is_refused(
    client, provider, db_session
):
    before = await db_session.scalar(select(func.count()).select_from(User))

    response = await _sign_in(
        client, provider, sub="stranger", email="stranger@example.com"
    )

    assert _refusal(response) == "OIDC_NO_ACCOUNT"
    assert not client.cookies.get(config.SESSION_COOKIE_NAME)
    after = await db_session.scalar(select(func.count()).select_from(User))
    assert after == before


async def test_an_unverified_email_is_not_linked(client, provider, person, db_session):
    response = await _sign_in(
        client, provider, sub="sub-x", email=person.email, email_verified=False
    )

    assert _refusal(response) == "OIDC_EMAIL_NOT_VERIFIED"
    assert await _links(db_session, person.id) == []


async def test_an_account_whose_address_was_never_verified_is_not_linked(
    client, provider, person, db_session
):
    # Someone signed up with (or once switched an account to) this address;
    # its real owner's first SSO sign-in must not land in that account.
    person.is_verified = False
    await db_session.flush()

    response = await _sign_in(client, provider, sub="sub-v", email=person.email)

    assert _refusal(response) == "OIDC_ACCOUNT_NOT_VERIFIED"
    assert await _links(db_session, person.id) == []
    assert not client.cookies.get(config.SESSION_COOKIE_NAME)


async def test_a_deactivated_account_is_refused(client, provider, person, db_session):
    person.is_active = False
    await db_session.flush()

    response = await _sign_in(client, provider, sub="sub-y", email=person.email)

    assert _refusal(response) == "USER_INACTIVE"


async def test_a_callback_from_another_browser_is_refused(client, provider, person):
    start = await client.get("/auth/oidc/authorize-redirect")
    callback = provider.approve(
        start.headers["location"], sub="sub-z", email=person.email
    )
    client.cookies.clear()  # the link opened somewhere else

    response = await client.get(callback)

    assert _refusal(response) == "OIDC_INVALID_STATE"
    assert provider.token_requests == []  # the code was never even spent


async def test_a_forged_state_is_refused(client, provider):
    await client.get("/auth/oidc/authorize-redirect")

    response = await client.get("/auth/oidc/callback?code=x&state=bogus")

    assert _refusal(response) == "OIDC_INVALID_STATE"


async def test_a_refusal_at_the_provider_is_explained(client):
    response = await client.get("/auth/oidc/callback?error=access_denied&state=s")

    assert _refusal(response) == "access_denied"


async def test_a_refusal_at_the_provider_in_the_browser_that_started_is_explained(
    client,
):
    start = await client.get("/auth/oidc/authorize-redirect")
    state = parse_qs(urlsplit(start.headers["location"]).query)["state"][0]

    response = await client.get(
        "/auth/oidc/callback", params={"error": "access_denied", "state": state}
    )

    assert _refusal(response) == "access_denied"


async def test_a_code_without_this_browsers_cookie_is_still_refused_as_invalid_state(
    client,
):
    # The provider-error tolerance above is for ?error= only.
    response = await client.get("/auth/oidc/callback?code=x&state=s")

    assert _refusal(response) == "OIDC_INVALID_STATE"


async def test_an_app_sign_in_refused_at_the_provider_goes_back_to_the_app(
    db_session, provider, oauth_table
):
    from app.auth_oidc.app_routes import build_app_sign_in_router
    from app.auth_oidc.app_sign_in import MemoryAppCodes, challenge_of

    settings = _settings()
    codes = MemoryAppCodes()
    directory = ProviderDirectory(DISCOVERY_URL, transport=provider.transport)
    app = FastAPI()
    app.include_router(
        build_oidc_router(
            settings,
            secret=SECRET,
            rate_limit=Attempts(),
            directory=directory,
            app_codes=codes,
        )
    )
    app.include_router(
        build_app_sign_in_router(
            settings,
            secret=SECRET,
            rate_limit=Attempts(),
            codes=codes,
            directory=directory,
        )
    )

    async def same_session() -> AsyncGenerator[AsyncSession, None]:
        yield db_session

    app.dependency_overrides[get_async_session] = same_session
    async with httpx.AsyncClient(
        transport=ASGITransport(app=app), base_url=NOTES
    ) as http:
        start = await http.get(
            "/api/v1/auth/oidc/app/start",
            params={
                "app_redirect": "soloclef://auth",
                "challenge": challenge_of("v" * 43),
                "challenge_method": "S256",
            },
        )
        assert start.status_code == 302, start.text
        state = parse_qs(urlsplit(start.headers["location"]).query)["state"][0]
        response = await http.get(
            "/auth/oidc/callback", params={"error": "access_denied", "state": state}
        )

    location = urlsplit(response.headers["location"])
    assert f"{location.scheme}://{location.netloc}" == "soloclef://auth"
    assert parse_qs(location.query) == {"error": ["access_denied"]}


async def test_an_id_token_for_another_client_is_refused(client, provider, person):
    provider.id_token_overrides = {"aud": "some-other-app"}

    response = await _sign_in(client, provider, sub="sub-a", email=person.email)

    assert _refusal(response) == "OIDC_INVALID_ID_TOKEN"
    assert not client.cookies.get(config.SESSION_COOKIE_NAME)


async def test_signup_when_allowed_makes_a_verified_account(
    db_session, provider, oauth_table
):
    settings = _settings(allow_signup=True)
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        response = await _sign_in(
            http, provider, sub="new-person", email="newcomer@example.com"
        )

    assert response.status_code == 303
    user = await db_session.scalar(
        select(User).where(User.email == "newcomer@example.com")
    )
    assert user is not None and user.is_verified
    assert len(await _links(db_session, user.id)) == 1


async def _user_count(db_session) -> int:
    return await db_session.scalar(select(func.count()).select_from(User))


async def test_a_first_sign_in_at_an_autocreate_domain_makes_the_account_as_sign_up_does(
    db_session, provider, oauth_table, sign_up_side_effects
):
    settings = _settings(autocreate_domains=AUTOCREATE_DOMAINS)  # OIDC_ALLOW_SIGNUP stays off
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        response = await _sign_in(
            http, provider, sub="authentik-ifee", email="Ifee@Example.com", name="Ifee"
        )
        signed_in = http.cookies.get(config.SESSION_COOKIE_NAME)

    assert response.status_code == 303
    assert response.headers["location"] == f"{NOTES}/dashboard"
    assert signed_in
    user = await db_session.scalar(select(User).where(User.email == "ifee@example.com"))
    assert user is not None
    assert (user.is_active, user.is_verified, user.is_superuser) == (True, True, False)
    assert user.display_name == "Ifee"
    # Nobody knows the password; "Forgot password?" sets one.
    assert user.hashed_password
    ok, _ = PasswordHelper().verify_and_update("password123", user.hashed_password)
    assert not ok
    # What on_after_register gives every new account.
    workspaces = (
        await db_session.scalars(select(Workspace).where(Workspace.user_id == user.id))
    ).all()
    assert [w.name for w in workspaces] == ["My Workspace"]
    membership = await db_session.scalar(
        select(WorkspaceMembership).where(WorkspaceMembership.user_id == user.id)
    )
    assert membership.workspace_id == workspaces[0].id and membership.is_owner
    owner_role = await db_session.get(WorkspaceRole, membership.role_id)
    assert owner_role.name == "Owner"
    prompts = await db_session.scalar(
        select(func.count()).select_from(Prompt).where(Prompt.user_id == user.id)
    )
    assert prompts == len(SYSTEM_PROMPT_DEFAULTS)
    links = await _links(db_session, user.id)
    assert [(row["oauth_name"], row["account_id"]) for row in links] == [
        ("authentik", "authentik-ifee")
    ]
    assert links[0]["account_email"] == "ifee@example.com"


async def test_the_next_sign_in_reuses_the_account_it_made(
    db_session, provider, oauth_table, sign_up_side_effects
):
    settings = _settings(autocreate_domains=AUTOCREATE_DOMAINS)
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        await _sign_in(http, provider, sub="authentik-new", email="new@example.com")
        after_first = await _user_count(db_session)
        http.cookies.clear()
        again = await _sign_in(
            http, provider, sub="authentik-new", email="new@example.com"
        )

    assert again.status_code == 303
    assert await _user_count(db_session) == after_first
    user = await db_session.scalar(select(User).where(User.email == "new@example.com"))
    assert len(await _links(db_session, user.id)) == 1


async def test_an_address_at_another_domain_is_refused_with_autocreate_on(
    db_session, provider, oauth_table, sign_up_side_effects
):
    before = await _user_count(db_session)
    settings = _settings(autocreate_domains=AUTOCREATE_DOMAINS)
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        response = await _sign_in(
            http, provider, sub="gmail-friend", email="friend@gmail.com"
        )
        signed_in = http.cookies.get(config.SESSION_COOKIE_NAME)

    assert _refusal(response) == "OIDC_NO_ACCOUNT"
    assert not signed_in
    assert await _user_count(db_session) == before


async def test_a_look_alike_domain_is_refused(
    db_session, provider, oauth_table, sign_up_side_effects
):
    before = await _user_count(db_session)
    settings = _settings(autocreate_domains=AUTOCREATE_DOMAINS)
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        response = await _sign_in(
            http, provider, sub="look-alike", email="someone@sub.example.com"
        )

    assert _refusal(response) == "OIDC_NO_ACCOUNT"
    assert await _user_count(db_session) == before


async def test_an_existing_account_at_an_autocreate_domain_is_linked_not_duplicated(
    db_session, provider, oauth_table, sign_up_side_effects
):
    operator = User(
        id=uuid.uuid4(),
        email="you@example.com",
        hashed_password=PasswordHelper().hash("a password nobody types"),
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(operator)
    await db_session.flush()
    before = await _user_count(db_session)

    settings = _settings(autocreate_domains=AUTOCREATE_DOMAINS)
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        response = await _sign_in(
            http, provider, sub="authentik-operator", email="you@example.com"
        )

    assert response.status_code == 303
    assert await _user_count(db_session) == before
    assert len(await _links(db_session, operator.id)) == 1


async def test_a_simultaneous_first_sign_in_links_to_the_account_the_other_made(
    db_session, provider, oauth_table, sign_up_side_effects, monkeypatch
):
    """Two tabs: the other one created the account between lookup and create."""
    other_tab = {}

    async def made_by_the_other_tab(self, user_create, safe=False, request=None):
        user = User(
            id=uuid.uuid4(),
            email=user_create.email,
            hashed_password=PasswordHelper().hash("a password nobody types"),
            is_active=True,
            is_superuser=False,
            is_verified=True,
        )
        db_session.add(user)
        await db_session.flush()
        other_tab["user"] = user
        raise UserAlreadyExists()

    monkeypatch.setattr(UserManager, "create", made_by_the_other_tab)
    settings = _settings(autocreate_domains=AUTOCREATE_DOMAINS)
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        response = await _sign_in(
            http, provider, sub="authentik-twotabs", email="twotabs@example.com"
        )

    assert response.status_code == 303
    links = await _links(db_session, other_tab["user"].id)
    assert [row["account_id"] for row in links] == ["authentik-twotabs"]


async def test_an_account_signed_up_in_the_same_moment_is_not_linked(
    db_session, provider, oauth_table, sign_up_side_effects, monkeypatch
):
    """The password sign-up form made an unverified account with this address
    between the lookup and the creation: refused as one found at the lookup."""
    planted = {}

    async def signed_up_meanwhile(self, user_create, safe=False, request=None):
        user = User(
            id=uuid.uuid4(),
            email=user_create.email,
            hashed_password=PasswordHelper().hash("the planter's password"),
            is_active=True,
            is_superuser=False,
            is_verified=False,
        )
        db_session.add(user)
        await db_session.flush()
        planted["user"] = user
        raise UserAlreadyExists()

    monkeypatch.setattr(UserManager, "create", signed_up_meanwhile)
    settings = _settings(autocreate_domains=AUTOCREATE_DOMAINS)
    async with _client_for(db_session, provider, settings, Attempts()) as http:
        response = await _sign_in(
            http, provider, sub="authentik-raced", email="raced@example.com"
        )
        signed_in = http.cookies.get(config.SESSION_COOKIE_NAME)

    assert _refusal(response) == "OIDC_ACCOUNT_NOT_VERIFIED"
    assert not signed_in
    assert await _links(db_session, planted["user"].id) == []


async def test_too_many_attempts_from_one_address_are_turned_away(
    db_session, provider, oauth_table
):
    async with _client_for(db_session, provider, _settings(), Attempts(1)) as http:
        first = await http.get("/auth/oidc/authorize-redirect")
        second = await http.get("/auth/oidc/authorize-redirect")

    assert first.status_code == 302
    assert _refusal(second) == "RATE_LIMIT_EXCEEDED"


async def test_an_unreachable_provider_is_explained(db_session, oauth_table):
    def down(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    class Down(FakeOidcProvider):
        @property
        def transport(self):
            return httpx.MockTransport(down)

    async with _client_for(db_session, Down(), _settings(), Attempts()) as http:
        response = await http.get("/auth/oidc/authorize-redirect")

    assert _refusal(response) == "OIDC_UNAVAILABLE"
