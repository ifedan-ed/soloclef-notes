"""Wrap the SDK's MCP endpoint with identity + CORS for the remote transport.

CORS is outermost so keyless browser preflight is answered before the identity
middleware. ``/health`` is a public path, exempt from the key check, and so is
the protected-resource document when agent sign-in is on (claude.ai and
ChatGPT read it before they have a token). The site's proxy must route
``/.well-known/oauth-protected-resource*`` here as well as ``/mcp*``
(docker/proxy/Caddyfile).
"""

from __future__ import annotations

from mcp.server.fastmcp import FastMCP
from starlette.middleware.cors import CORSMiddleware
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.types import ASGIApp

from ...config import Settings
from ..auth.middleware import ApiKeyIdentityMiddleware
from ..auth.protected_resource import ProtectedResource
from ..auth.sign_in_check import BackendSignInCheck

HEALTH_PATH = "/health"


async def _health(_request: Request) -> JSONResponse:
    return JSONResponse({"status": "ok"})


def protected_resource_for(settings: Settings) -> ProtectedResource | None:
    if not settings.oauth_enabled:
        return None
    return ProtectedResource(
        resource=settings.public_url or "",
        authorization_server=settings.authorization_server or "",
        scopes=settings.oauth_scopes,
    )


def build_http_app(
    mcp: FastMCP,
    settings: Settings | None = None,
    *,
    sign_in_check: BackendSignInCheck | None = None,
) -> ASGIApp:
    """Return the MCP streamable-http app wrapped with identity + CORS."""
    mcp.custom_route(HEALTH_PATH, methods=["GET"])(_health)
    public_paths = {HEALTH_PATH}

    resource = protected_resource_for(settings) if settings is not None else None
    if resource is not None:

        async def _metadata(_request: Request) -> JSONResponse:
            return JSONResponse(resource.metadata())

        for path in resource.metadata_paths:
            mcp.custom_route(path, methods=["GET"])(_metadata)
            public_paths.add(path)
        sign_in_check = sign_in_check or BackendSignInCheck(settings.base_url)

    app: ASGIApp = ApiKeyIdentityMiddleware(
        mcp.streamable_http_app(),
        public_paths=public_paths,
        resource=resource,
        sign_in_check=sign_in_check,
    )
    return CORSMiddleware(
        app,
        allow_origins=["*"],
        allow_methods=["GET", "POST", "DELETE", "OPTIONS"],
        allow_headers=["*"],
        # Browser-based clients read the challenge to find where to sign in.
        expose_headers=["Mcp-Session-Id", "WWW-Authenticate"],
    )
