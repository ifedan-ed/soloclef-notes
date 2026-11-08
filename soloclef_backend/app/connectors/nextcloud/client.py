"""A read-only WebDAV client for one Nextcloud account.

It lists folders, finds files by name and downloads them. It can only send
PROPFIND, SEARCH and GET: there is no code path that writes to Nextcloud.

Nextcloud answers bursts and repeated failed logins with 429 (brute-force
protection) and a busy server with 503; both are retried a few times, honoring
``Retry-After`` up to a cap, and a small per-client concurrency limit keeps an
import of a large folder from hammering the server.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import os
import tempfile
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from xml.sax.saxutils import escape

import httpx

from .remote_paths import ROOT, dav_url, normalize_remote_path
from .webdav_listing import (
    REQUESTED_PROPS,
    RemoteEntry,
    folder_children,
    parse_multistatus,
)

logger = logging.getLogger(__name__)

_READ_ONLY_METHODS = frozenset({"PROPFIND", "SEARCH", "GET"})
_RETRYABLE = frozenset({429, 502, 503, 504})
_NAMESPACES = 'xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns"'
_PROPFIND_BODY = (
    f'<?xml version="1.0" encoding="UTF-8"?><d:propfind {_NAMESPACES}>'
    f"<d:prop>{REQUESTED_PROPS}</d:prop></d:propfind>"
)

DEFAULT_TIMEOUT = httpx.Timeout(30.0, connect=10.0)
DOWNLOAD_TIMEOUT = httpx.Timeout(120.0, connect=10.0)


class NextcloudError(Exception):
    """Base for everything the client raises; the message is safe to show."""


class NextcloudAuthError(NextcloudError):
    """The server refused the username/app password."""


class NextcloudNotFoundError(NextcloudError):
    """The path does not exist (or is not visible to this account)."""


class NextcloudUnavailableError(NextcloudError):
    """The server could not be reached, timed out or kept asking us to slow down."""


class NextcloudFileTooLargeError(NextcloudError):
    """A download went past the size limit and was abandoned."""


@dataclass(frozen=True)
class NextcloudAccount:
    base_url: str
    #: The Nextcloud user id: it names the WebDAV files root.
    username: str
    app_password: str
    #: What the app password was issued to; differs from the user id when
    #: people sign in with an email address. None means the user id.
    login_name: str | None = None

    @property
    def auth_name(self) -> str:
        return self.login_name or self.username


Sleep = Callable[[float], Awaitable[None]]


class NextcloudClient:
    def __init__(
        self,
        account: NextcloudAccount,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        max_concurrency: int = 4,
        max_attempts: int = 3,
        max_retry_wait: float = 15.0,
        sleep: Sleep = asyncio.sleep,
    ) -> None:
        self._account = account
        self._transport = transport
        self._semaphore = asyncio.Semaphore(max_concurrency)
        self._max_attempts = max_attempts
        self._max_retry_wait = max_retry_wait
        self._sleep = sleep

    @property
    def account(self) -> NextcloudAccount:
        return self._account

    def _http(self, timeout: httpx.Timeout) -> httpx.AsyncClient:
        return httpx.AsyncClient(
            auth=(self._account.auth_name, self._account.app_password),
            timeout=timeout,
            transport=self._transport,
            follow_redirects=False,
            headers={"OCS-APIRequest": "true", "User-Agent": "Soloclef Notes"},
        )

    def _retry_wait(self, response: httpx.Response, attempt: int) -> float:
        header = response.headers.get("Retry-After", "")
        try:
            wait = float(header)
        except ValueError:
            wait = 2.0**attempt
        return max(0.0, min(wait, self._max_retry_wait))

    @staticmethod
    def _raise_for(response: httpx.Response, path: str) -> None:
        if response.status_code in (401, 403):
            raise NextcloudAuthError("Nextcloud refused the username or app password")
        if response.status_code == 404:
            raise NextcloudNotFoundError(f"Not found in Nextcloud: {path}")
        if response.status_code >= 400:
            raise NextcloudUnavailableError(
                f"Nextcloud answered {response.status_code} for {path}"
            )

    async def _request(
        self,
        method: str,
        url: str,
        path: str,
        *,
        headers: dict[str, str] | None = None,
        content: str | None = None,
    ) -> httpx.Response:
        if method not in _READ_ONLY_METHODS:
            raise ValueError(f"{method} is not a read-only WebDAV method")
        async with self._semaphore:
            for attempt in range(1, self._max_attempts + 1):
                try:
                    async with self._http(DEFAULT_TIMEOUT) as http:
                        response = await http.request(
                            method, url, headers=headers, content=content
                        )
                except httpx.TimeoutException as exc:
                    if attempt == self._max_attempts:
                        raise NextcloudUnavailableError(
                            "Nextcloud did not answer in time"
                        ) from exc
                    await self._sleep(2.0**attempt)
                    continue
                except httpx.HTTPError as exc:
                    raise NextcloudUnavailableError(
                        f"Could not reach Nextcloud: {type(exc).__name__}"
                    ) from exc
                if response.status_code in _RETRYABLE and attempt < self._max_attempts:
                    await self._sleep(self._retry_wait(response, attempt))
                    continue
                self._raise_for(response, path)
                return response
        raise NextcloudUnavailableError("Nextcloud kept asking to slow down")

    async def _propfind(self, path: str, depth: str) -> list[RemoteEntry]:
        url = dav_url(self._account.base_url, self._account.username, path)
        response = await self._request(
            "PROPFIND",
            url,
            path,
            headers={"Depth": depth, "Content-Type": "application/xml"},
            content=_PROPFIND_BODY,
        )
        return parse_multistatus(response.content, self._account.username)

    async def stat(self, path: str) -> RemoteEntry:
        """The file or folder at ``path``."""
        path = normalize_remote_path(path)
        for entry in await self._propfind(path, "0"):
            if entry.path == path:
                return entry
        raise NextcloudNotFoundError(f"Not found in Nextcloud: {path}")

    async def list_folder(self, path: str = ROOT) -> list[RemoteEntry]:
        """The folder's direct children, folders first."""
        path = normalize_remote_path(path)
        return folder_children(await self._propfind(path, "1"), path)

    async def search(self, query: str, *, limit: int = 50) -> list[RemoteEntry]:
        """Files and folders anywhere in the account whose name contains ``query``."""
        term = query.strip().replace("%", "").replace("_", "")
        if not term:
            return []
        user = escape(self._account.username)
        body = (
            f'<?xml version="1.0" encoding="UTF-8"?><d:searchrequest {_NAMESPACES}>'
            f"<d:basicsearch><d:select><d:prop>{REQUESTED_PROPS}</d:prop></d:select>"
            f"<d:from><d:scope><d:href>/files/{user}</d:href>"
            "<d:depth>infinity</d:depth></d:scope></d:from>"
            f"<d:where><d:like><d:prop><d:displayname/></d:prop>"
            f"<d:literal>%{escape(term)}%</d:literal></d:like></d:where>"
            f"<d:orderby/><d:limit><d:nresults>{int(limit)}</d:nresults></d:limit>"
            "</d:basicsearch></d:searchrequest>"
        )
        url = self._account.base_url.rstrip("/") + "/remote.php/dav/"
        response = await self._request(
            "SEARCH", url, ROOT, headers={"Content-Type": "text/xml"}, content=body
        )
        entries = parse_multistatus(response.content, self._account.username)
        return sorted(
            (entry for entry in entries if entry.path != ROOT),
            key=lambda e: (not e.is_folder, e.name.casefold()),
        )[:limit]

    async def walk_files(
        self, folder: str, *, max_files: int, max_folders: int = 500
    ) -> tuple[list[RemoteEntry], bool]:
        """Every file under ``folder``, breadth first; True when a limit cut it short.

        Depth-1 listings rather than ``Depth: infinity``, which Nextcloud
        usually refuses and which would send the whole tree in one reply.
        """
        pending = [normalize_remote_path(folder)]
        files: list[RemoteEntry] = []
        visited = 0
        while pending:
            if visited >= max_folders:
                return files, True
            current = pending.pop(0)
            visited += 1
            for entry in await self.list_folder(current):
                if entry.is_folder:
                    pending.append(entry.path)
                    continue
                if len(files) >= max_files:
                    return files, True
                files.append(entry)
        return files, False

    async def download(self, path: str, *, max_bytes: int) -> str:
        """Stream the file to a temporary file and return its path; the caller removes it."""
        path = normalize_remote_path(path)
        url = dav_url(self._account.base_url, self._account.username, path)
        suffix = os.path.splitext(path)[1][:16]
        async with self._semaphore:
            for attempt in range(1, self._max_attempts + 1):
                temp_path: str | None = None
                try:
                    async with (
                        self._http(DOWNLOAD_TIMEOUT) as http,
                        http.stream("GET", url) as response,
                    ):
                        if (
                            response.status_code in _RETRYABLE
                            and attempt < self._max_attempts
                        ):
                            wait = self._retry_wait(response, attempt)
                        else:
                            self._raise_for(response, path)
                            temp_path = await self._save(
                                response, path, suffix, max_bytes
                            )
                            return temp_path
                except BaseException as exc:
                    if temp_path is not None:
                        with contextlib.suppress(OSError):
                            os.unlink(temp_path)
                    if isinstance(exc, httpx.TimeoutException):
                        raise NextcloudUnavailableError(
                            "Nextcloud did not send the file in time"
                        ) from exc
                    if isinstance(exc, httpx.HTTPError):
                        raise NextcloudUnavailableError(
                            f"Could not download from Nextcloud: {type(exc).__name__}"
                        ) from exc
                    raise
                await self._sleep(wait)
        raise NextcloudUnavailableError("Nextcloud kept asking to slow down")

    @staticmethod
    async def _save(
        response: httpx.Response, path: str, suffix: str, max_bytes: int
    ) -> str:
        fd, temp_path = tempfile.mkstemp(suffix=suffix, prefix="nextcloud-")
        written = 0
        try:
            with os.fdopen(fd, "wb") as out:
                async for chunk in response.aiter_bytes():
                    written += len(chunk)
                    if written > max_bytes:
                        raise NextcloudFileTooLargeError(
                            f"{path} is larger than the "
                            f"{max_bytes // (1024 * 1024)} MB limit"
                        )
                    out.write(chunk)
        except BaseException:
            with contextlib.suppress(OSError):
                os.unlink(temp_path)
            raise
        return temp_path
