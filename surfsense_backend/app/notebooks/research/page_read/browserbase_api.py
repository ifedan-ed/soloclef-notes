"""Browserbase's REST API: start a browser session, and always end it.

Only two calls are made: ``POST /v1/sessions`` (a fresh cloud browser, with a
short hard ``timeout`` so Browserbase ends it even if we never could) and
``POST /v1/sessions/{id}`` with ``status: REQUEST_RELEASE`` to end it as soon
as the page is read. The live test found sessions left running by the MCP
server; :func:`browserbase_session` releases in a ``finally``, whatever
happened in between.

``projectId`` is optional for keys that belong to one project; when the API
asks for it, the first project the key can see is used (``GET /v1/projects``)
and remembered for the process.

The HTTP client is injectable (``transport``) so tests stand a fake
Browserbase in at the HTTP boundary. ``BROWSERBASE_API_URL`` points the whole
thing at another host (the e2e stack's stand-in).
"""

from __future__ import annotations

import logging
import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass

import httpx

logger = logging.getLogger(__name__)

DEFAULT_API_URL = "https://api.browserbase.com"
# Browserbase ends the session itself after this many seconds (its minimum is
# 60), so a crash between start and release costs at most this long.
SESSION_TIMEOUT_S = 90
HTTP_TIMEOUT_S = 20.0

_project_ids: dict[str, str] = {}


class BrowserbaseError(Exception):
    """Browserbase refused or could not be reached. ``code`` is for the UI."""

    def __init__(self, message: str, *, code: str = "browserbase_unavailable"):
        super().__init__(message)
        self.code = code


@dataclass(frozen=True)
class BrowserSession:
    id: str
    connect_url: str
    project_id: str | None


def api_url() -> str:
    return (os.getenv("BROWSERBASE_API_URL") or DEFAULT_API_URL).rstrip("/")


class BrowserbaseApi:
    def __init__(
        self,
        api_key: str,
        *,
        base_url: str | None = None,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self._key = api_key
        self._base = (base_url or api_url()).rstrip("/")
        self._transport = transport

    def _client(self) -> httpx.AsyncClient:
        return httpx.AsyncClient(
            base_url=self._base,
            headers={"X-BB-API-Key": self._key, "Content-Type": "application/json"},
            timeout=HTTP_TIMEOUT_S,
            transport=self._transport,
            # The API never needs to redirect; do not follow one anywhere.
            follow_redirects=False,
            trust_env=False,
        )

    async def create_session(self) -> BrowserSession:
        body: dict = {"timeout": SESSION_TIMEOUT_S, "keepAlive": False}
        project_id = _project_ids.get(self._key)
        if project_id:
            body["projectId"] = project_id
        async with self._client() as client:
            response = await self._post(client, "/v1/sessions", body)
            if response.status_code in (400, 422) and "projectId" not in body:
                # This key needs a project named explicitly.
                project_id = await self._first_project(client)
                if project_id:
                    body["projectId"] = project_id
                    response = await self._post(client, "/v1/sessions", body)
        self._raise_for(response)
        data = _json(response)
        session_id = data.get("id")
        connect_url = data.get("connectUrl")
        if not isinstance(session_id, str) or not isinstance(connect_url, str):
            raise BrowserbaseError("Browserbase started no usable session.")
        project = data.get("projectId") or body.get("projectId")
        if isinstance(project, str):
            _project_ids[self._key] = project
        return BrowserSession(
            session_id, connect_url, project if isinstance(project, str) else None
        )

    async def release_session(self, session: BrowserSession) -> None:
        """End the session now. Never raises; a failure is logged (Browserbase
        still ends it at ``SESSION_TIMEOUT_S``)."""
        body: dict = {"status": "REQUEST_RELEASE"}
        if session.project_id:
            body["projectId"] = session.project_id
        try:
            async with self._client() as client:
                response = await client.post(f"/v1/sessions/{session.id}", json=body)
            if response.status_code >= 400:
                logger.warning(
                    "Browserbase did not release session %s: HTTP %s",
                    session.id,
                    response.status_code,
                )
        except httpx.HTTPError as exc:
            logger.warning(
                "Browserbase release of session %s failed: %s",
                session.id,
                type(exc).__name__,
            )

    async def _post(self, client: httpx.AsyncClient, path: str, body: dict):
        try:
            return await client.post(path, json=body)
        except httpx.HTTPError as exc:
            raise BrowserbaseError(
                "Browserbase didn't answer just now. Try again in a minute."
            ) from exc

    async def _first_project(self, client: httpx.AsyncClient) -> str | None:
        try:
            response = await client.get("/v1/projects")
        except httpx.HTTPError:
            return None
        if response.status_code != 200:
            return None
        data = _json_any(response)
        projects = data if isinstance(data, list) else []
        for project in projects:
            if isinstance(project, dict) and isinstance(project.get("id"), str):
                return project["id"]
        return None

    @staticmethod
    def _raise_for(response: httpx.Response) -> None:
        status = response.status_code
        if status < 400:
            return
        if status in (401, 403):
            raise BrowserbaseError(
                "Browserbase refused this workspace's API key. Check the "
                "Browserbase connector.",
                code="browserbase_key_rejected",
            )
        if status in (402, 429):
            raise BrowserbaseError(
                "Browserbase turned the request down: the account is out of "
                "browser time or over its limit.",
                code="browserbase_quota",
            )
        raise BrowserbaseError(
            f"Browserbase couldn't start a browser (HTTP {status}).",
        )


def _json_any(response: httpx.Response):
    try:
        return response.json()
    except ValueError:
        return None


def _json(response: httpx.Response) -> dict:
    data = _json_any(response)
    return data if isinstance(data, dict) else {}


@asynccontextmanager
async def browserbase_session(api: BrowserbaseApi) -> AsyncIterator[BrowserSession]:
    """A Browserbase session that is released however the block ends."""
    session = await api.create_session()
    try:
        yield session
    finally:
        await api.release_session(session)
