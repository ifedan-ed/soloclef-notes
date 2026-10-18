"""Agent sign-in on the remote transport (MCP authorization spec).

Covers: the protected-resource document and the 401 challenge claude.ai and
ChatGPT read; API keys still pass untouched; a sign-in token is checked with
the backend once and then remembered; bad, expired and account-less sign-ins
are refused with the status a client can act on; and per-caller state keys on
the account, not on the hourly-refreshed token.
"""

from __future__ import annotations

import asyncio
import base64
import json
import time

import httpx
import pytest
from starlette.responses import JSONResponse

from mcp_server.config import Settings
from mcp_server.core.auth.identity import current_api_key, current_identity
from mcp_server.core.auth.middleware import ApiKeyIdentityMiddleware
from mcp_server.core.auth.protected_resource import ProtectedResource
from mcp_server.core.auth.sign_in_check import ACCEPT_CACHE_SECONDS, BackendSignInCheck

RESOURCE = ProtectedResource(
    resource="https://notes.example.com/mcp",
    authorization_server="https://sso.example.com/application/o/soloclef-mcp/",
    scopes=("openid", "email", "profile", "offline_access"),
)


def _jwt(exp: float) -> str:
    def part(obj: dict) -> str:
        return base64.urlsafe_b64encode(json.dumps(obj).encode()).decode().rstrip("=")

    return f"{part({'alg': 'RS256'})}.{part({'exp': exp, 'sub': 's'})}.sig"


class Backend:
    """Plays the backend's GET /verify-token."""

    def __init__(self) -> None:
        self.calls = 0
        self.answers: dict[str, httpx.Response] = {}

    def handler(self, request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/verify-token"
        self.calls += 1
        token = request.headers["authorization"].removeprefix("Bearer ")
        return self.answers.get(token, httpx.Response(401, json={"detail": "nope"}))


class Clock:
    def __init__(self) -> None:
        self.now = 100.0

    def __call__(self) -> float:
        return self.now


def _check(backend: Backend, clock: Clock | None = None) -> BackendSignInCheck:
    return BackendSignInCheck(
        "http://backend:8000",
        transport=httpx.MockTransport(backend.handler),
        clock=clock or Clock(),
    )


async def _echo(scope, receive, send):
    response = JSONResponse({"key": current_api_key(), "identity": current_identity()})
    await response(scope, receive, send)


def _app(check: BackendSignInCheck | None, resource=RESOURCE):
    return ApiKeyIdentityMiddleware(
        _echo, public_paths={"/health"}, resource=resource, sign_in_check=check
    )


def _get(app, headers: dict[str, str] | None = None) -> httpx.Response:
    async def run() -> httpx.Response:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app), base_url="https://notes.example.com"
        ) as client:
            return await client.post("/mcp", headers=headers or {}, json={})

    return asyncio.run(run())


# --- the document and the challenge ----------------------------------------


def test_metadata_names_this_server_and_authentik():
    assert RESOURCE.metadata() == {
        "resource": "https://notes.example.com/mcp",
        "authorization_servers": [
            "https://sso.example.com/application/o/soloclef-mcp/"
        ],
        "scopes_supported": ["openid", "email", "profile", "offline_access"],
        "bearer_methods_supported": ["header"],
        "resource_name": "Soloclef Notes",
    }
    assert RESOURCE.metadata_paths == (
        "/.well-known/oauth-protected-resource/mcp",
        "/.well-known/oauth-protected-resource",
    )
    assert RESOURCE.metadata_url == (
        "https://notes.example.com/.well-known/oauth-protected-resource/mcp"
    )


def test_no_credential_gets_a_401_pointing_at_the_metadata():
    response = _get(_app(_check(Backend())))
    assert response.status_code == 401
    challenge = response.headers["www-authenticate"]
    assert challenge.startswith("Bearer ")
    assert (
        'resource_metadata="https://notes.example.com/.well-known/'
        'oauth-protected-resource/mcp"' in challenge
    )
    assert 'scope="openid email profile offline_access"' in challenge


def test_without_sign_in_configured_the_old_401_is_kept():
    response = _get(_app(None, resource=None))
    assert response.status_code == 401
    assert "www-authenticate" not in response.headers


# --- API keys ----------------------------------------------------------------


def test_api_keys_pass_without_asking_the_backend():
    backend = Backend()
    response = _get(_app(_check(backend)), {"Authorization": "Bearer ss_pat_abc"})
    assert response.status_code == 200
    assert response.json() == {"key": "ss_pat_abc", "identity": "ss_pat_abc"}
    assert backend.calls == 0
    response = _get(_app(_check(backend)), {"X-API-Key": "ss_pat_xyz"})
    assert response.json()["key"] == "ss_pat_xyz"
    assert backend.calls == 0


# --- sign-in tokens ----------------------------------------------------------


def test_a_good_sign_in_passes_and_is_remembered():
    backend = Backend()
    token = _jwt(time.time() + 3600)
    backend.answers[token] = httpx.Response(
        200, json={"method": "agent", "user_id": "u-1"}
    )
    clock = Clock()
    app = _app(_check(backend, clock))
    first = _get(app, {"Authorization": f"Bearer {token}"})
    assert first.status_code == 200
    assert first.json() == {"key": token, "identity": "user:u-1"}
    _get(app, {"Authorization": f"Bearer {token}"})
    assert backend.calls == 1
    clock.now += ACCEPT_CACHE_SECONDS + 1
    _get(app, {"Authorization": f"Bearer {token}"})
    assert backend.calls == 2


def test_a_token_about_to_expire_is_not_remembered_past_its_expiry():
    backend = Backend()
    token = _jwt(time.time() + 5)
    backend.answers[token] = httpx.Response(
        200, json={"method": "agent", "user_id": "u-1"}
    )
    clock = Clock()
    app = _app(_check(backend, clock))
    _get(app, {"Authorization": f"Bearer {token}"})
    clock.now += 6
    _get(app, {"Authorization": f"Bearer {token}"})
    assert backend.calls == 2


def test_an_invalid_or_expired_sign_in_is_401_invalid_token():
    backend = Backend()
    response = _get(
        _app(_check(backend)), {"Authorization": f"Bearer {_jwt(time.time() - 10)}"}
    )
    assert response.status_code == 401
    assert 'error="invalid_token"' in response.headers["www-authenticate"]
    assert "resource_metadata=" in response.headers["www-authenticate"]


def test_a_web_session_token_is_not_an_agent_sign_in():
    backend = Backend()
    backend.answers["session-jwt"] = httpx.Response(
        200, json={"method": "session", "user_id": "u-1"}
    )
    response = _get(_app(_check(backend)), {"Authorization": "Bearer session-jwt"})
    assert response.status_code == 401


def test_a_sign_in_without_a_notes_account_is_403_without_a_challenge():
    backend = Backend()
    backend.answers["stranger"] = httpx.Response(
        403, json={"detail": "No Soloclef Notes account matches this sign-in"}
    )
    response = _get(_app(_check(backend)), {"Authorization": "Bearer stranger"})
    assert response.status_code == 403
    assert "www-authenticate" not in response.headers
    assert "No Soloclef Notes account" in response.json()["message"]


def test_backend_down_is_503_not_a_sign_in_loop():
    def down(_request):
        raise httpx.ConnectError("refused")

    check = BackendSignInCheck(
        "http://backend:8000", transport=httpx.MockTransport(down)
    )
    response = _get(_app(check), {"Authorization": "Bearer something"})
    assert response.status_code == 503
    assert "www-authenticate" not in response.headers


@pytest.mark.parametrize("status", [500, 502, 503, 504, 404])
def test_a_backend_that_cannot_decide_is_503_not_invalid_token(status):
    backend = Backend()
    backend.answers["good-token"] = httpx.Response(
        status, json={"detail": "Single sign-on cannot be reached right now"}
    )
    response = _get(_app(_check(backend)), {"Authorization": "Bearer good-token"})
    assert response.status_code == 503
    assert "www-authenticate" not in response.headers
    assert response.json()["error"] == "temporarily_unavailable"


def test_a_backend_error_page_without_json_is_still_503():
    backend = Backend()
    backend.answers["good-token"] = httpx.Response(502, text="<html>Bad Gateway")
    response = _get(_app(_check(backend)), {"Authorization": "Bearer good-token"})
    assert response.status_code == 503
    assert response.json()["message"]


def test_settings_turn_sign_in_on_from_the_shared_env(monkeypatch):
    monkeypatch.setenv(
        "MCP_OAUTH_ISSUER", "https://sso.example.com/application/o/soloclef-mcp/"
    )
    monkeypatch.setenv("SURFSENSE_PUBLIC_URL", "https://notes.example.com/")
    monkeypatch.delenv("SURFSENSE_MCP_PUBLIC_URL", raising=False)
    settings = Settings.from_env()
    assert settings.oauth_enabled
    assert settings.public_url == "https://notes.example.com/mcp"
    assert settings.oauth_scopes == ("openid", "email", "profile", "offline_access")

    monkeypatch.delenv("MCP_OAUTH_ISSUER")
    assert not Settings.from_env().oauth_enabled


# --- the real app ------------------------------------------------------------


def test_the_served_app_publishes_the_document_and_challenges(monkeypatch):
    from mcp_server.core.transport import build_http_app
    from mcp_server.server import build_server

    monkeypatch.setenv(
        "MCP_OAUTH_ISSUER", "https://sso.example.com/application/o/soloclef-mcp/"
    )
    monkeypatch.setenv("SURFSENSE_MCP_PUBLIC_URL", "https://notes.example.com/mcp")
    settings = Settings.from_env()
    mcp, _client = build_server(settings)
    app = build_http_app(mcp, settings, sign_in_check=_check(Backend()))

    async def run():
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app), base_url="https://notes.example.com"
        ) as client:
            meta = await client.get("/.well-known/oauth-protected-resource/mcp")
            root = await client.get("/.well-known/oauth-protected-resource")
            denied = await client.post("/mcp", json={})
            preflight = await client.options(
                "/mcp",
                headers={
                    "Origin": "https://claude.ai",
                    "Access-Control-Request-Method": "POST",
                },
            )
            return meta, root, denied, preflight

    meta, root, denied, preflight = asyncio.run(run())
    assert meta.status_code == 200
    assert meta.json()["resource"] == "https://notes.example.com/mcp"
    assert meta.json()["authorization_servers"] == [
        "https://sso.example.com/application/o/soloclef-mcp/"
    ]
    assert root.json() == meta.json()
    assert denied.status_code == 401
    assert "resource_metadata=" in denied.headers["www-authenticate"]
    assert preflight.status_code == 200
