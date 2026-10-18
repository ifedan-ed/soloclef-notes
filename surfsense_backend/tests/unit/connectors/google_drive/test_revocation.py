"""Disconnect revokes the stored grant at Google (best effort)."""

import httpx
import pytest

from app.connectors.google_drive.connect.revocation import revoke_stored_grant
from app.connectors.google_drive.connect.stored_grant import replace_grant

pytestmark = pytest.mark.unit

GRANT = {"token": "ya29.access", "refresh_token": "1//refresh", "client_secret": "s"}


def _google(status: int, seen: list):
    def handle(request: httpx.Request) -> httpx.Response:
        seen.append((str(request.url), request.content.decode()))
        return httpx.Response(status, json={})

    return httpx.MockTransport(handle)


async def test_the_decrypted_refresh_token_is_revoked():
    seen: list = []
    revoked = await revoke_stored_grant(
        replace_grant(None, GRANT, "a@example.com"), transport=_google(200, seen)
    )
    assert revoked is True
    assert seen == [("https://oauth2.googleapis.com/revoke", "token=1%2F%2Frefresh")]


async def test_google_refusing_is_reported_not_raised():
    seen: list = []
    stored = replace_grant(None, GRANT, None)
    assert await revoke_stored_grant(stored, transport=_google(400, seen)) is False


async def test_nothing_stored_means_nothing_to_revoke():
    assert await revoke_stored_grant({}, transport=_google(200, [])) is False
