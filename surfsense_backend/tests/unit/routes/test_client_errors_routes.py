"""POST /api/v1/client-errors: auth, size cap, rate limit, what gets logged."""

from __future__ import annotations

import json
import logging
from types import SimpleNamespace
from uuid import UUID

import pytest
from fastapi import FastAPI, HTTPException
from starlette.testclient import TestClient

import app.routes.client_errors_routes as route
from app.auth.context import AuthContext
from app.users import require_session_context

pytestmark = pytest.mark.unit

USER = UUID("11111111-2222-3333-4444-555555555555")


def _client(*, signed_in: bool = True, user_id: UUID = USER) -> TestClient:
    app = FastAPI()
    app.include_router(route.router, prefix="/api/v1")

    async def _session() -> AuthContext:
        if not signed_in:
            raise HTTPException(status_code=401, detail="Unauthorized")
        return AuthContext.session(SimpleNamespace(id=user_id))

    app.dependency_overrides[require_session_context] = _session
    return TestClient(app)


@pytest.fixture(autouse=True)
def _fresh_limits():
    route._limiter.reset()
    yield
    route._limiter.reset()


def _report(**overrides) -> dict:
    body = {
        "kind": "error-boundary",
        "name": "TypeError",
        "message": "Cannot read properties of undefined (reading 'map')",
        "stack": "TypeError: x\n    at Notebook (page.js:1:2)\n    at render (react.js:3:4)",
        "path": "/dashboard/1/notebooks/3?token=abcdef123456#frag",
        "digest": "12345",
        "build": "abc1234",
    }
    body.update(overrides)
    return body


def test_requires_a_session() -> None:
    res = _client(signed_in=False).post("/api/v1/client-errors", json=_report())
    assert res.status_code == 401


def test_logs_one_warning_with_path_build_and_stack(caplog) -> None:
    with caplog.at_level(logging.WARNING, logger="surfsense.client"):
        res = _client().post("/api/v1/client-errors", json=_report())
    assert res.status_code == 204
    records = [r for r in caplog.records if r.name == "surfsense.client"]
    assert len(records) == 1
    msg = records[0].getMessage()
    assert records[0].levelno == logging.WARNING
    assert "kind=error-boundary" in msg
    assert "path=/dashboard/1/notebooks/3 " in msg
    assert "abcdef123456" not in msg and "frag" not in msg
    assert "build=abc1234" in msg and "digest=12345" in msg
    assert "TypeError: Cannot read properties" in msg
    # Stack lines are indented and prefixed, never bare log-looking lines.
    stack_lines = msg.split("\n")[1:]
    assert stack_lines and all(line.startswith("    | ") for line in stack_lines)


def test_message_cannot_forge_log_lines(caplog) -> None:
    forged = "oops\n2026-01-01T00:00:00.000Z ERROR app.auth admin logged in"
    with caplog.at_level(logging.WARNING, logger="surfsense.client"):
        _client().post("/api/v1/client-errors", json=_report(message=forged, stack=""))
    msg = next(r for r in caplog.records if r.name == "surfsense.client").getMessage()
    assert "\n" not in msg


def test_rejects_oversized_body() -> None:
    big = json.dumps(_report(stack="x" * (route.MAX_BODY_BYTES + 10)))
    res = _client().post(
        "/api/v1/client-errors",
        content=big,
        headers={"Content-Type": "application/json"},
    )
    assert res.status_code == 413


def test_rejects_unknown_kind() -> None:
    res = _client().post("/api/v1/client-errors", json=_report(kind="anything"))
    assert res.status_code == 422


def test_rate_limited_per_user() -> None:
    client = _client()
    codes = [
        client.post("/api/v1/client-errors", json=_report()).status_code
        for _ in range(route.PER_USER_LIMIT + 2)
    ]
    assert codes[: route.PER_USER_LIMIT] == [204] * route.PER_USER_LIMIT
    assert codes[-1] == 429
    # Another user still has budget.
    other = _client(user_id=UUID("99999999-2222-3333-4444-555555555555"))
    assert other.post("/api/v1/client-errors", json=_report()).status_code == 204
