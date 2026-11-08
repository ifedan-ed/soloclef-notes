"""The crawler fallback's redirect walk never connects to a private address."""

from __future__ import annotations

import httpx
import pytest

from app.notebooks.research.page_read.public_http import (
    NotPublicError,
    final_public_url,
)

pytestmark = pytest.mark.unit

PUBLIC = "https://93.184.215.14"


def _client(routes: dict[str, httpx.Response]) -> httpx.AsyncClient:
    def handle(request: httpx.Request) -> httpx.Response:
        return routes.get(str(request.url), httpx.Response(200, text="ok"))

    return httpx.AsyncClient(
        transport=httpx.MockTransport(handle), follow_redirects=False
    )


@pytest.mark.parametrize(
    "url",
    [
        "http://127.0.0.1/",
        "http://10.1.2.3/x",
        "http://169.254.169.254/latest/meta-data/",
        "http://[::1]/",
        "ftp://93.184.215.14/file",
        "https://user:pw@93.184.215.14/",
    ],
)
async def test_non_public_or_odd_addresses_are_refused(url):
    with pytest.raises(NotPublicError):
        await final_public_url(url, client=_client({}))


async def test_a_redirect_to_a_private_address_is_refused():
    client = _client(
        {
            f"{PUBLIC}/start": httpx.Response(
                302, headers={"location": "http://192.168.1.1/router"}
            )
        }
    )
    with pytest.raises(NotPublicError):
        await final_public_url(f"{PUBLIC}/start", client=client)


async def test_public_redirects_are_followed_to_where_they_land():
    client = _client(
        {
            f"{PUBLIC}/a": httpx.Response(301, headers={"location": "/b"}),
            f"{PUBLIC}/b": httpx.Response(302, headers={"location": f"{PUBLIC}/c"}),
        }
    )
    assert await final_public_url(f"{PUBLIC}/a", client=client) == f"{PUBLIC}/c"


async def test_endless_redirects_stop():
    client = _client(
        {f"{PUBLIC}/loop": httpx.Response(302, headers={"location": "/loop"})}
    )
    with pytest.raises(NotPublicError):
        await final_public_url(f"{PUBLIC}/loop", client=client)


async def test_the_pinned_client_will_not_connect_to_a_private_address():
    from app.notebooks.research.page_read.public_http import public_only_client

    async with public_only_client() as client:
        with pytest.raises(httpx.ConnectError):
            await client.get("http://127.0.0.1:9/")
