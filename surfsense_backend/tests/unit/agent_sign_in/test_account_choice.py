"""Which Notes account an agent's sign-in acts as (read-only, never creates one)."""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest

from app.agent_sign_in.access_token import AgentIdentity
from app.agent_sign_in.account import (
    NoAccountForAgentError,
    account_for_agent,
    choose_account,
)

pytestmark = pytest.mark.unit

LINKED_ACCOUNT_ID = uuid.uuid4()
EMAIL_ACCOUNT_ID = uuid.uuid4()


def test_a_linked_identity_acts_as_its_account_whatever_its_email():
    assert (
        choose_account(
            linked_user_id=LINKED_ACCOUNT_ID, email_user_id=EMAIL_ACCOUNT_ID, email_verified=False
        )
        == LINKED_ACCOUNT_ID
    )


def test_otherwise_the_account_with_the_same_verified_email():
    assert (
        choose_account(
            linked_user_id=None,
            email_user_id=EMAIL_ACCOUNT_ID,
            email_verified=True,
            email_user_verified=True,
        )
        == EMAIL_ACCOUNT_ID
    )


def test_an_account_whose_own_address_was_never_verified_matches_nobody():
    with pytest.raises(NoAccountForAgentError):
        choose_account(
            linked_user_id=None,
            email_user_id=EMAIL_ACCOUNT_ID,
            email_verified=True,
            email_user_verified=False,
        )


def test_an_unverified_email_matches_nobody():
    with pytest.raises(NoAccountForAgentError):
        choose_account(linked_user_id=None, email_user_id=EMAIL_ACCOUNT_ID, email_verified=False)


def test_no_account_means_no_access():
    with pytest.raises(NoAccountForAgentError):
        choose_account(linked_user_id=None, email_user_id=None, email_verified=True)


class FakeSession:
    """Answers the two lookups in order: the subject link, then the email."""

    def __init__(self, *, linked=None, by_email=None, users=()):
        self._answers = [linked, by_email]
        self._users = {u.id: u for u in users}
        self.lookups = 0

    async def scalar(self, _statement):
        self.lookups += 1
        return self._answers.pop(0)

    async def get(self, _model, user_id):
        return self._users.get(user_id)


def user(user_id, email, active=True, verified=True):
    return SimpleNamespace(
        id=user_id, email=email, is_active=active, is_verified=verified
    )


def identity(email, verified=True, subject="sub-x"):
    return AgentIdentity(subject=subject, email=email, email_verified=verified)


async def test_the_linked_account_wins_over_the_email_account():
    accounts = (
        user(LINKED_ACCOUNT_ID, "linked@example.com"),
        user(EMAIL_ACCOUNT_ID, "email@example.com"),
    )
    found = await account_for_agent(
        identity("linked@example.com"),
        provider_name="authentik",
        session=FakeSession(by_email=LINKED_ACCOUNT_ID, users=accounts),
    )
    assert found.id == LINKED_ACCOUNT_ID
    found = await account_for_agent(
        identity("email@example.com"),
        provider_name="authentik",
        session=FakeSession(by_email=EMAIL_ACCOUNT_ID, users=accounts),
    )
    assert found.id == EMAIL_ACCOUNT_ID


async def test_a_linked_subject_skips_the_email_lookup():
    session = FakeSession(linked=LINKED_ACCOUNT_ID, users=(user(LINKED_ACCOUNT_ID, "linked@example.com"),))
    found = await account_for_agent(
        identity("someone-else@example.com"),
        provider_name="authentik",
        session=session,
    )
    assert found.id == LINKED_ACCOUNT_ID
    assert session.lookups == 1


async def test_a_deactivated_account_is_refused():
    with pytest.raises(NoAccountForAgentError):
        await account_for_agent(
            identity("linked@example.com"),
            provider_name="authentik",
            session=FakeSession(
                by_email=LINKED_ACCOUNT_ID, users=(user(LINKED_ACCOUNT_ID, "d@x", active=False),)
            ),
        )


async def test_an_unverified_account_with_the_email_is_not_acted_as():
    with pytest.raises(NoAccountForAgentError):
        await account_for_agent(
            identity("linked@example.com"),
            provider_name="authentik",
            session=FakeSession(
                by_email=LINKED_ACCOUNT_ID,
                users=(user(LINKED_ACCOUNT_ID, "you@example.com", verified=False),),
            ),
        )
