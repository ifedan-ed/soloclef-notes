"""Talking to Nextcloud's Login Flow v2, OCS user and app password endpoints."""

import httpx
import pytest

from app.connectors.nextcloud.login_flow.login_api import (
    NextcloudLoginApi,
    NextcloudLoginError,
    NextcloudLoginRefusedError,
)

from ..fake_login_server import BASE, FakeLoginServer

pytestmark = pytest.mark.unit


async def test_a_login_is_started_and_finished_once_granted():
    server = FakeLoginServer()
    api = server.api()

    started = await api.start()
    assert started.login_url.startswith(f"{BASE}/login/v2/flow/")
    assert await api.poll(started.poll_endpoint, started.poll_token) is None

    password = server.grant(started.poll_token)
    granted = await api.poll(started.poll_endpoint, started.poll_token)
    assert granted.login_name == "alice@example.com"
    assert granted.app_password == password
    # Nextcloud hands it over once.
    assert await api.poll(started.poll_endpoint, started.poll_token) is None
    assert server.requests[0].headers["User-Agent"] == "Soloclef Notes"


async def test_the_poll_token_is_sent_only_in_the_body():
    server = FakeLoginServer()
    api = server.api()
    started = await api.start()
    await api.poll(started.poll_endpoint, started.poll_token)
    poll = server.requests[-1]
    assert started.poll_token not in str(poll.url)
    assert f"token={started.poll_token}" in poll.content.decode()


async def test_a_poll_endpoint_on_another_server_is_refused():
    server = FakeLoginServer()
    server.poll_endpoint = "https://attacker.example.net/login/v2/poll"
    with pytest.raises(NextcloudLoginError):
        await server.api().start()
    with pytest.raises(NextcloudLoginError):
        await server.api().poll("https://attacker.example.net/poll", "t")


async def test_the_account_is_named_by_its_user_id():
    server = FakeLoginServer(login_name="bob@example.com", user_id="Bob Smith")
    api = server.api()
    started = await api.start()
    password = server.grant(started.poll_token)
    identity = await api.identity("bob@example.com", password)
    assert identity.user_id == "Bob Smith"
    assert identity.display_name == "Alice Liddell"
    with pytest.raises(NextcloudLoginRefusedError):
        await api.identity("bob@example.com", "wrong")


async def test_revoking_deletes_the_app_password():
    server = FakeLoginServer()
    api = server.api()
    started = await api.start()
    password = server.grant(started.poll_token)
    assert await api.revoke("alice@example.com", password) is True
    assert server.revoked == [password]
    # Already gone counts as revoked.
    assert await api.revoke("alice@example.com", password) is True


async def test_revoking_reports_a_refusal_or_an_unreachable_server():
    refused = NextcloudLoginApi(
        BASE, transport=httpx.MockTransport(lambda r: httpx.Response(403))
    )
    assert await refused.revoke("alice", "real-password") is False

    def down(request):
        raise httpx.ConnectError("down", request=request)

    unreachable = NextcloudLoginApi(BASE, transport=httpx.MockTransport(down))
    assert await unreachable.revoke("alice", "pw") is False


async def test_an_unexpected_start_reply_is_an_error():
    api = NextcloudLoginApi(
        BASE, transport=httpx.MockTransport(lambda r: httpx.Response(200, json={}))
    )
    with pytest.raises(NextcloudLoginError):
        await api.start()
    javascript = NextcloudLoginApi(
        BASE,
        transport=httpx.MockTransport(
            lambda r: httpx.Response(
                200,
                json={
                    "login": "javascript:alert(1)",
                    "poll": {"token": "t", "endpoint": f"{BASE}/login/v2/poll"},
                },
            )
        ),
    )
    with pytest.raises(NextcloudLoginError):
        await javascript.start()
