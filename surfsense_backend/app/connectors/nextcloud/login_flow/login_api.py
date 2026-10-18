"""Nextcloud's side of connecting: Login Flow v2, who the account is, revoking it.

Only the configured server is ever contacted. Nextcloud answers the poll with
404 until the person grants access; the app password it then returns is kept
in memory only long enough to be checked and encrypted.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from urllib.parse import urlsplit

import httpx

logger = logging.getLogger(__name__)

TIMEOUT = httpx.Timeout(15.0, connect=10.0)
USER_AGENT = "Soloclef Notes"


class NextcloudLoginError(Exception):
    """Nextcloud could not be reached or answered something unusable; safe to show."""


class NextcloudLoginRefusedError(NextcloudLoginError):
    """Nextcloud refused the app password it had just issued."""


@dataclass(frozen=True)
class StartedLogin:
    login_url: str
    poll_endpoint: str
    poll_token: str


@dataclass(frozen=True)
class GrantedLogin:
    login_name: str
    app_password: str


@dataclass(frozen=True)
class NextcloudIdentity:
    user_id: str
    display_name: str


def _same_origin(url: str, base_url: str) -> bool:
    a, b = urlsplit(url), urlsplit(base_url)
    return (a.scheme, a.netloc) == (b.scheme, b.netloc)


class NextcloudLoginApi:
    def __init__(
        self, base_url: str, *, transport: httpx.AsyncBaseTransport | None = None
    ) -> None:
        self._base = base_url.rstrip("/")
        self._transport = transport

    def _http(self, auth: tuple[str, str] | None = None) -> httpx.AsyncClient:
        return httpx.AsyncClient(
            auth=auth,
            timeout=TIMEOUT,
            transport=self._transport,
            follow_redirects=False,
            headers={"User-Agent": USER_AGENT, "OCS-APIRequest": "true"},
        )

    async def _send(
        self, method: str, url: str, *, auth=None, data=None, params=None
    ) -> httpx.Response:
        try:
            async with self._http(auth) as http:
                return await http.request(method, url, data=data, params=params)
        except httpx.HTTPError as exc:
            raise NextcloudLoginError(
                f"Could not reach Nextcloud: {type(exc).__name__}"
            ) from exc

    async def start(self) -> StartedLogin:
        """Open a login flow; its URL is for the person, the poll pair stays here."""
        response = await self._send("POST", f"{self._base}/index.php/login/v2")
        if response.status_code != 200:
            raise NextcloudLoginError(
                f"Nextcloud answered {response.status_code} to the login request"
            )
        try:
            body = response.json()
            login_url = str(body["login"])
            poll_endpoint = str(body["poll"]["endpoint"])
            poll_token = str(body["poll"]["token"])
        except (ValueError, KeyError, TypeError) as exc:
            raise NextcloudLoginError(
                "Nextcloud sent an unexpected login reply"
            ) from exc
        if urlsplit(login_url).scheme not in ("http", "https") or not poll_token:
            raise NextcloudLoginError("Nextcloud sent an unexpected login reply")
        # The poll carries the token, so it may only go back to the same server.
        if not _same_origin(poll_endpoint, self._base):
            raise NextcloudLoginError("Nextcloud named a different server to poll")
        return StartedLogin(login_url, poll_endpoint, poll_token)

    async def poll(self, endpoint: str, token: str) -> GrantedLogin | None:
        """The granted login, or None while the person has not finished.

        Nextcloud hands the app password over once; a second poll is a 404.
        """
        if not _same_origin(endpoint, self._base):
            raise NextcloudLoginError("Refusing to poll another server")
        response = await self._send("POST", endpoint, data={"token": token})
        if response.status_code == 404:
            return None
        if response.status_code != 200:
            raise NextcloudLoginError(
                f"Nextcloud answered {response.status_code} while connecting"
            )
        try:
            body = response.json()
            login_name = str(body["loginName"])
            app_password = str(body["appPassword"])
        except (ValueError, KeyError, TypeError) as exc:
            raise NextcloudLoginError(
                "Nextcloud sent an unexpected login reply"
            ) from exc
        if not login_name or not app_password:
            raise NextcloudLoginError("Nextcloud sent an empty login")
        return GrantedLogin(login_name, app_password)

    async def identity(self, login_name: str, app_password: str) -> NextcloudIdentity:
        """The account's user id (it names the WebDAV root) and display name."""
        response = await self._send(
            "GET",
            f"{self._base}/ocs/v2.php/cloud/user",
            auth=(login_name, app_password),
            params={"format": "json"},
        )
        if response.status_code in (401, 403):
            raise NextcloudLoginRefusedError("Nextcloud refused the new app password")
        if response.status_code != 200:
            raise NextcloudLoginError(
                f"Nextcloud answered {response.status_code} for the account"
            )
        try:
            data = response.json()["ocs"]["data"]
            user_id = str(data["id"])
            display = str(data.get("display-name") or data.get("displayname") or "")
        except (ValueError, KeyError, TypeError) as exc:
            raise NextcloudLoginError(
                "Nextcloud sent an unexpected account reply"
            ) from exc
        if not user_id:
            raise NextcloudLoginError("Nextcloud sent no user id")
        return NextcloudIdentity(user_id=user_id, display_name=display or user_id)

    async def revoke(self, login_name: str, app_password: str) -> bool:
        """Delete this app password in Nextcloud; False when it could not be."""
        try:
            response = await self._send(
                "DELETE",
                f"{self._base}/ocs/v2.php/core/apppassword",
                auth=(login_name, app_password),
            )
        except NextcloudLoginError:
            logger.warning("Nextcloud app password revoke: server unreachable")
            return False
        # 401: already gone. 403: a real password, not an app password.
        if response.status_code == 401:
            return True
        if response.status_code != 200:
            logger.warning(
                "Nextcloud app password revoke answered %s", response.status_code
            )
            return False
        return True
