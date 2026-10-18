"""c0030 on real Postgres: accounts SSO already signed in to count as verified.

The owner's own account was unverified with an SSO link, so after the
2026-10-11 rule ("link or act by email only for a verified account") his
agent sign-in, which matches by email when its subject was never linked,
would have been refused. The revision verifies exactly the active accounts
whose SSO link vouches for their current address; agent sign-in then acts
as them. Rolls back.
"""

from __future__ import annotations

import importlib.util
import uuid
from pathlib import Path

import pytest
from alembic.operations import Operations
from alembic.runtime.migration import MigrationContext
from sqlalchemy import text

from app.agent_sign_in.access_token import AgentIdentity
from app.agent_sign_in.account import NoAccountForAgentError, account_for_agent
from app.auth_oidc.account_link import identity_links
from app.db import User

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[2]
    / "alembic"
    / "versions"
    / "c0030_verify_sso_linked_accounts.py"
)

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


def _upgrade(session):
    spec = importlib.util.spec_from_file_location("c0030", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)

    def apply(sync_session):
        module.op = Operations(MigrationContext.configure(sync_session.connection()))
        module.upgrade()

    return session.run_sync(apply)


async def _person(session, email, *, verified=False, active=True, link=None):
    user = User(
        id=uuid.uuid4(),
        email=email,
        hashed_password="x",
        is_active=active,
        is_superuser=False,
        is_verified=verified,
    )
    session.add(user)
    await session.flush()
    if link is not None:
        provider, vouched = link
        await session.execute(
            identity_links.insert().values(
                id=uuid.uuid4(),
                user_id=user.id,
                oauth_name=provider,
                access_token="",
                account_id=f"web-{uuid.uuid4().hex[:6]}",
                account_email=vouched,
            )
        )
    return user


async def _verified(session, user) -> bool:
    return await session.scalar(
        text('SELECT is_verified FROM "user" WHERE id = :id'), {"id": user.id}
    )


async def test_only_active_accounts_whose_sso_link_vouches_for_them_are_verified(
    db_session, monkeypatch
):
    monkeypatch.delenv("OIDC_PROVIDER_NAME", raising=False)
    await db_session.execute(text(OAUTH_ACCOUNT_DDL))
    owner = await _person(
        db_session, "owner@example.test", link=("authentik", "Owner@Example.test")
    )
    renamed = await _person(
        db_session, "planted@example.test", link=("authentik", "someone@example.test")
    )
    gone = await _person(
        db_session,
        "gone@example.test",
        active=False,
        link=("authentik", "gone@example.test"),
    )
    never = await _person(db_session, "signup@example.test")
    google = await _person(
        db_session, "google@example.test", link=("google", "google@example.test")
    )
    already = await _person(
        db_session,
        "ok@example.test",
        verified=True,
        link=("authentik", "ok@example.test"),
    )

    await _upgrade(db_session)

    assert await _verified(db_session, owner)
    assert await _verified(db_session, already)
    for left in (renamed, gone, never, google):
        assert not await _verified(db_session, left), left.email


async def test_agent_sign_in_acts_for_an_sso_linked_account_after_it(
    db_session, monkeypatch
):
    monkeypatch.delenv("OIDC_PROVIDER_NAME", raising=False)
    await db_session.execute(text(OAUTH_ACCOUNT_DDL))
    owner = await _person(
        db_session, "owner@example.test", link=("authentik", "owner@example.test")
    )
    # The agent's own Authentik application: a subject the web never linked.
    agent = AgentIdentity(
        subject="agent-subject", email="owner@example.test", email_verified=True
    )
    with pytest.raises(NoAccountForAgentError):
        await account_for_agent(agent, provider_name="authentik", session=db_session)

    await _upgrade(db_session)
    db_session.expire_all()

    acting = await account_for_agent(
        agent, provider_name="authentik", session=db_session
    )
    assert acting.id == owner.id


async def test_a_database_without_sso_links_is_left_alone(db_session):
    exists = await db_session.scalar(text("SELECT to_regclass('oauth_account')"))
    if exists is not None:
        pytest.skip("this test database has an oauth_account table")
    await _upgrade(db_session)
