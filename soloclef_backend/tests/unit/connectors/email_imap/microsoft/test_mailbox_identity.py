"""Which mailbox an id_token names, from the claims Microsoft actually sends."""

from __future__ import annotations

import pytest

from app.connectors.email_imap.microsoft.mailbox_identity import (
    UnknownMailboxError,
    mailbox_from_id_token,
)
from tests.unit.connectors.email_imap.microsoft.fake_microsoft_login import (
    CLIENT_ID,
    MAILBOX,
    SignedInAccount,
)

pytestmark = pytest.mark.unit

ASKED = {"openid", "email", "profile", "offline_access"}


def test_the_email_claim_and_the_object_id_name_the_mailbox():
    mailbox = mailbox_from_id_token(SignedInAccount().id_token(ASKED), CLIENT_ID)
    assert mailbox.address == MAILBOX
    assert mailbox.subject == "tid-1:oid-1"


def test_without_an_email_claim_the_sign_in_name_is_used():
    account = SignedInAccount(email=None, preferred_username="user@corp.example")
    mailbox = mailbox_from_id_token(account.id_token(ASKED), CLIENT_ID)
    assert mailbox.address == "user@corp.example"


def test_without_the_profile_scope_there_is_no_fallback_and_no_object_id():
    # What Microsoft sends when profile isn't asked for: why SCOPES has it.
    without_profile = ASKED - {"profile"}
    mailbox = mailbox_from_id_token(
        SignedInAccount().id_token(without_profile), CLIENT_ID
    )
    assert mailbox.subject == "tid-1:subject-1"
    with pytest.raises(UnknownMailboxError):
        mailbox_from_id_token(
            SignedInAccount(email=None).id_token(without_profile), CLIENT_ID
        )


def test_an_id_token_for_another_app_is_refused():
    with pytest.raises(UnknownMailboxError):
        mailbox_from_id_token(SignedInAccount().id_token(ASKED), "another-app")
