"""The Report Issue address: none by default, SUPPORT_EMAIL, admin-only edits,
public read."""

from __future__ import annotations

import pytest

from app.support_contact import DEFAULT_SUPPORT_EMAIL

pytestmark = pytest.mark.integration

PATH = "/api/v1/support-contact"
SETTINGS = "/api/v1/billing/admin/settings"


@pytest.fixture(autouse=True)
def _no_support_email_env(monkeypatch):
    monkeypatch.delenv("SUPPORT_EMAIL", raising=False)


async def test_anyone_reads_the_default_without_signing_in(api):
    async with api(None) as client:
        response = await client.get(PATH)
    assert response.status_code == 200
    assert response.json() == {"email": DEFAULT_SUPPORT_EMAIL}
    # No server's address is a default: none is published until one is set.
    assert DEFAULT_SUPPORT_EMAIL == ""


async def test_support_email_from_the_environment_is_published(api, monkeypatch):
    monkeypatch.setenv("SUPPORT_EMAIL", "help@example.org")
    async with api(None) as client:
        assert (await client.get(PATH)).json() == {"email": "help@example.org"}


async def test_an_admin_changes_it_and_everyone_sees_the_change(new_user, api):
    admin = await new_user("owner@surfsense.net")
    async with api(admin) as client:
        saved = await client.put(
            SETTINGS, json={"values": {"support_email": "  help@example.org "}}
        )
        assert saved.status_code == 200, saved.text
        assert saved.json()["values"]["support_email"] == "help@example.org"
    async with api(None) as client:
        assert (await client.get(PATH)).json() == {"email": "help@example.org"}


@pytest.mark.parametrize(
    "bad", ["", "not-an-email", "a@b", "x@y.z, evil@z.io", "a@b.c?cc=x@y.z", 7]
)
async def test_only_one_plain_address_is_accepted(new_user, api, bad):
    admin = await new_user("owner@surfsense.net")
    async with api(admin) as client:
        response = await client.put(SETTINGS, json={"values": {"support_email": bad}})
        assert response.status_code == 400
    async with api(None) as client:
        assert (await client.get(PATH)).json()["email"] == DEFAULT_SUPPORT_EMAIL


async def test_a_member_cannot_change_it(db_user, api):
    async with api(db_user) as client:
        response = await client.put(
            SETTINGS, json={"values": {"support_email": "evil@example.org"}}
        )
        assert response.status_code == 403
    async with api(None) as client:
        assert (await client.get(PATH)).json()["email"] == DEFAULT_SUPPORT_EMAIL
