"""A fake Nextcloud for connecting: Login Flow v2, the OCS user and app password APIs, WebDAV root.

It behaves like the real server where it matters: the poll answers 404 until
``grant()``, hands the app password over once, and only an app password it
issued (and has not revoked) opens the OCS and WebDAV endpoints.
"""

from __future__ import annotations

import base64
from urllib.parse import parse_qs, unquote, urlsplit

import httpx

from app.connectors.nextcloud.client import NextcloudAccount, NextcloudClient
from app.connectors.nextcloud.login_flow.login_api import NextcloudLoginApi

from .fake_server import fixture

BASE = "https://cloud.example.com"


class FakeLoginServer:
    def __init__(
        self,
        *,
        login_name: str = "alice@example.com",
        user_id: str = "alice",
        display_name: str = "Alice Liddell",
    ) -> None:
        self.login_name = login_name
        self.user_id = user_id
        self.display_name = display_name
        self.requests: list[httpx.Request] = []
        self.flows: dict[str, dict] = {}
        self.valid_passwords: set[str] = set()
        self.revoked: list[str] = []
        self.poll_endpoint = f"{BASE}/login/v2/poll"
        self.dav_status = 207
        self._issued = 0

    # -- driving the fake -------------------------------------------------
    def grant(self, token: str | None = None) -> str:
        """The person signs in and grants access; returns the app password."""
        token = token or next(iter(self.flows))
        self._issued += 1
        password = f"app-password-{self._issued}"
        self.flows[token]["password"] = password
        self.valid_passwords.add(password)
        return password

    def api(self, base_url: str = BASE) -> NextcloudLoginApi:
        return NextcloudLoginApi(base_url, transport=httpx.MockTransport(self.handler))

    def webdav(self, account: NextcloudAccount) -> NextcloudClient:
        return NextcloudClient(account, transport=httpx.MockTransport(self.handler))

    # -- the server -------------------------------------------------------
    def _password(self, request: httpx.Request) -> str | None:
        header = request.headers.get("Authorization", "")
        if not header.startswith("Basic "):
            return None
        user, _, password = base64.b64decode(header[6:]).decode().partition(":")
        if user != self.login_name or password not in self.valid_passwords:
            return None
        return password

    def handler(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        path = unquote(urlsplit(str(request.url)).path)
        if request.method == "POST" and path == "/index.php/login/v2":
            token = f"poll-token-{len(self.flows) + 1}"
            self.flows[token] = {"password": None}
            return httpx.Response(
                200,
                json={
                    "poll": {"token": token, "endpoint": self.poll_endpoint},
                    "login": f"{BASE}/login/v2/flow/login-{len(self.flows)}",
                },
            )
        if request.method == "POST" and path == "/login/v2/poll":
            token = parse_qs(request.content.decode()).get("token", [""])[0]
            flow = self.flows.get(token)
            if not flow or not flow["password"]:
                return httpx.Response(404, json=[])
            del self.flows[token]  # handed over once
            return httpx.Response(
                200,
                json={
                    "server": BASE,
                    "loginName": self.login_name,
                    "appPassword": flow["password"],
                },
            )
        if request.method == "GET" and path == "/ocs/v2.php/cloud/user":
            if self._password(request) is None:
                return httpx.Response(401)
            return httpx.Response(
                200,
                json={
                    "ocs": {
                        "data": {"id": self.user_id, "display-name": self.display_name}
                    }
                },
            )
        if request.method == "DELETE" and path == "/ocs/v2.php/core/apppassword":
            password = self._password(request)
            if password is None:
                return httpx.Response(401)
            self.valid_passwords.discard(password)
            self.revoked.append(password)
            return httpx.Response(200, json={"ocs": {"data": []}})
        if request.method == "PROPFIND" and path.startswith(
            f"/remote.php/dav/files/{self.user_id}"
        ):
            if self._password(request) is None:
                return httpx.Response(401)
            if self.dav_status != 207:
                return httpx.Response(self.dav_status)
            return httpx.Response(207, content=fixture("propfind_root.xml"))
        return httpx.Response(404)
