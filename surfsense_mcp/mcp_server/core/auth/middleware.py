"""ASGI middleware that establishes the caller's identity for each request.

A pure ASGI middleware, deliberately not Starlette's ``BaseHTTPMiddleware``:
the latter runs the endpoint in a separate task, so a contextvar set in it does
not reach the tool handler. A pure middleware binds the key in the request's own
task, from which the SDK's per-request handling inherits it.

Two kinds of caller get through:

- An API key (``ss_pat_...``, as Claude Code, Cursor and the other coding
  agents send it) passes as before; the backend checks it on every tool call.
- With agent sign-in on (``resource`` and ``sign_in_check`` given), any other
  bearer token is an Authentik access token from claude.ai or ChatGPT. The
  backend is asked once whether it is valid before the request goes on, so a
  bad or expired one gets a 401 the client can act on.

Requests without a credential are rejected here so no tool ever runs
unauthenticated; with sign-in on, the 401 carries the ``WWW-Authenticate``
challenge that tells claude.ai and ChatGPT where to sign in. Paths in
``public_paths`` (the health probe, the protected-resource document) skip the
check entirely.
"""

from __future__ import annotations

from collections.abc import Iterable

from starlette.datastructures import Headers
from starlette.responses import JSONResponse
from starlette.types import ASGIApp, Receive, Scope, Send

from .headers import extract_api_key
from .identity import bind_api_key, unbind_api_key
from .protected_resource import ProtectedResource
from .sign_in_check import BackendSignInCheck

API_KEY_PREFIX = "ss_pat_"


class ApiKeyIdentityMiddleware:
    """Binds the per-request credential into the identity contextvar, or refuses."""

    def __init__(
        self,
        app: ASGIApp,
        public_paths: Iterable[str] = (),
        *,
        resource: ProtectedResource | None = None,
        sign_in_check: BackendSignInCheck | None = None,
    ) -> None:
        self._app = app
        self._public_paths = frozenset(public_paths)
        self._resource = resource
        self._sign_in_check = sign_in_check if resource is not None else None

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http" or scope["path"] in self._public_paths:
            await self._app(scope, receive, send)
            return

        credential = extract_api_key(Headers(scope=scope))
        if credential is None:
            await self._unauthenticated()(scope, receive, send)
            return

        identity = None
        if self._sign_in_check is not None and not credential.startswith(
            API_KEY_PREFIX
        ):
            verdict = await self._sign_in_check.check(credential)
            if not verdict.accepted:
                await self._refused(verdict.status, verdict.message)(
                    scope, receive, send
                )
                return
            identity = verdict.identity

        token = bind_api_key(credential, identity)
        try:
            await self._app(scope, receive, send)
        finally:
            unbind_api_key(token)

    def _unauthenticated(self) -> JSONResponse:
        if self._resource is None:
            return JSONResponse(
                {
                    "error": "unauthorized",
                    "message": (
                        "Missing Soloclef Notes API key. Send 'Authorization: "
                        "Bearer ss_pat_...' (or an X-API-Key header)."
                    ),
                },
                status_code=401,
            )
        return JSONResponse(
            {
                "error": "unauthorized",
                "message": (
                    "Sign in to Soloclef Notes, or send a Soloclef Notes API key "
                    "as 'Authorization: Bearer ss_pat_...'."
                ),
            },
            status_code=401,
            headers={"WWW-Authenticate": self._resource.challenge()},
        )

    def _refused(self, status: int, message: str) -> JSONResponse:
        if status == 401 and self._resource is not None:
            description = message or "The sign-in is not valid or has expired"
            return JSONResponse(
                {"error": "invalid_token", "message": description},
                status_code=401,
                headers={
                    "WWW-Authenticate": self._resource.challenge(
                        "invalid_token", description
                    )
                },
            )
        if status == 403:
            # A genuine sign-in for someone with no Notes account: no challenge,
            # since signing in again as the same person cannot fix it.
            return JSONResponse(
                {
                    "error": "access_denied",
                    "message": message
                    or "No Soloclef Notes account matches this sign-in",
                },
                status_code=403,
            )
        return JSONResponse(
            {"error": "temporarily_unavailable", "message": message},
            status_code=503,
        )
