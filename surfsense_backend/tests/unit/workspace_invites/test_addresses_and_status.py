from __future__ import annotations

from datetime import UTC, datetime, timedelta
from types import SimpleNamespace

import pytest

from app.workspace_invites.addresses import mask_address, same_address
from app.workspace_invites.message import build_invite_email
from app.workspace_invites.status import invite_status

pytestmark = pytest.mark.unit


def test_addresses_match_whatever_their_case_and_spacing():
    assert same_address(" operator@Example.com", "operator@example.com")
    assert not same_address("operator@example.com", "email@example.com")
    assert not same_address(None, "a@b.c")


def test_masked_address_keeps_the_domain_and_first_letter():
    assert mask_address("you@example.com") == "y**@example.com"
    assert mask_address("a@x.org") == "a*@x.org"
    assert mask_address(None) is None


def _invite(**kw):
    base = {
        "email": "a@b.c",
        "max_uses": 1,
        "uses_count": 0,
        "is_active": True,
        "expires_at": None,
    }
    return SimpleNamespace(**{**base, **kw})


def test_status_reads_use_activity_and_expiry():
    now = datetime.now(UTC)
    assert invite_status(_invite()) == "pending"
    assert invite_status(_invite(uses_count=1)) == "used_up"
    assert invite_status(_invite(is_active=False)) == "revoked"
    assert invite_status(_invite(expires_at=now - timedelta(seconds=1))) == "expired"
    assert invite_status(_invite(email=None, max_uses=None, uses_count=9)) == "pending"


def test_email_names_inviter_workspace_role_and_escapes_html():
    mail = build_invite_email(
        to="a@b.c",
        inviter="Dr <b>Ann</b>",
        workspace_name="Ward & Co",
        role_name="Viewer",
        invite_code="CODE",
        expires_at=None,
    )
    assert "Dr <b>Ann</b> invited you" in mail.text_body
    assert "/invite/CODE" in mail.text_body
    assert "&lt;b&gt;" in mail.html_body and "Ward &amp; Co" in mail.html_body
