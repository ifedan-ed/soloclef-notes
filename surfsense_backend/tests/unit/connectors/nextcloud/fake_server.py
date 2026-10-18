"""A recorded-reply WebDAV server for the Nextcloud client tests.

Replies come from the XML fixtures next to this file, keyed by method and
decoded path; every request is kept so tests can assert what was (and was
not) sent.
"""

from __future__ import annotations

from pathlib import Path
from urllib.parse import unquote, urlsplit

import httpx

from app.connectors.nextcloud.client import NextcloudAccount, NextcloudClient

FIXTURES = Path(__file__).parent / "fixtures"
ACCOUNT = NextcloudAccount(
    base_url="https://cloud.example.com", username="alice", app_password="app-pw"
)


def fixture(name: str) -> bytes:
    return (FIXTURES / name).read_bytes()


class FakeNextcloud:
    def __init__(self) -> None:
        self.routes: dict[tuple[str, str], list[httpx.Response]] = {}
        self.requests: list[httpx.Request] = []

    def reply(self, method: str, path: str, *responses: httpx.Response) -> None:
        self.routes[(method, path)] = list(responses)

    def handler(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        key = (request.method, unquote(urlsplit(str(request.url)).path))
        queue = self.routes.get(key)
        if not queue:
            return httpx.Response(404, content=fixture("error_not_found.xml"))
        return queue.pop(0) if len(queue) > 1 else queue[0]

    def client(self, **kwargs) -> NextcloudClient:
        async def no_sleep(seconds: float) -> None:
            self.waits.append(seconds)

        self.waits: list[float] = []
        return NextcloudClient(
            ACCOUNT,
            transport=httpx.MockTransport(self.handler),
            sleep=no_sleep,
            **kwargs,
        )


def multistatus(name: str) -> httpx.Response:
    return httpx.Response(207, content=fixture(name))


DAV = "/remote.php/dav/files/alice"
