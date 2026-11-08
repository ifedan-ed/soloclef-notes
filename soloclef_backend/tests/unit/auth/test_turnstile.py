"""Turnstile: off without a secret, and fails closed when it is on."""

from __future__ import annotations

import httpx
import pytest
from fastapi import HTTPException
from starlette.requests import Request

from app.auth import turnstile
from app.config import config

pytestmark = pytest.mark.unit


def _request(token: str | None = None) -> Request:
    headers = [(b"x-real-ip", b"203.0.113.9")]
    if token is not None:
        headers.append((turnstile.TOKEN_HEADER.lower().encode(), token.encode()))
    return Request(
        {
            "type": "http",
            "method": "POST",
            "path": "/auth/jwt/login",
            "headers": headers,
        }
    )


@pytest.fixture
def cloudflare(monkeypatch):
    """Answer siteverify like Cloudflare's test secrets; record what was sent."""
    seen: list[dict] = []
    state = {"mode": "pass"}

    async def handler(request: httpx.Request) -> httpx.Response:
        form = dict(httpx.QueryParams(request.content.decode()))
        seen.append(form)
        if state["mode"] == "down":
            return httpx.Response(502)
        if state["mode"] == "garbage":
            return httpx.Response(200, text="not json")
        ok = state["mode"] == "pass"
        return httpx.Response(
            200,
            json={
                "success": ok,
                "error-codes": [] if ok else ["invalid-input-response"],
            },
        )

    transport = httpx.MockTransport(handler)
    real_client = httpx.AsyncClient

    def client_factory(*args, **kwargs):
        kwargs["transport"] = transport
        return real_client(*args, **kwargs)

    monkeypatch.setattr(turnstile.httpx, "AsyncClient", client_factory)
    monkeypatch.setattr(
        config, "TURNSTILE_SECRET_KEY", "1x0000000000000000000000000000000AA"
    )
    return state, seen


async def test_without_a_secret_nothing_is_checked(monkeypatch):
    monkeypatch.setattr(config, "TURNSTILE_SECRET_KEY", "")
    await turnstile.require_turnstile(_request())


async def test_a_missing_token_is_refused_before_asking_cloudflare(cloudflare):
    _, seen = cloudflare
    with pytest.raises(HTTPException) as exc:
        await turnstile.require_turnstile(_request())
    assert (exc.value.status_code, exc.value.detail) == (400, "TURNSTILE_REQUIRED")
    with pytest.raises(HTTPException):
        await turnstile.require_turnstile(_request("x" * 5000))
    assert seen == []


async def test_a_valid_token_passes_and_sends_secret_token_and_ip(cloudflare):
    _, seen = cloudflare
    await turnstile.require_turnstile(_request("XXXX.DUMMY.TOKEN.XXXX"))
    assert seen == [
        {
            "secret": "1x0000000000000000000000000000000AA",
            "response": "XXXX.DUMMY.TOKEN.XXXX",
            "remoteip": "203.0.113.9",
        }
    ]


async def test_a_rejected_token_is_refused(cloudflare):
    state, _ = cloudflare
    state["mode"] = "fail"
    with pytest.raises(HTTPException) as exc:
        await turnstile.require_turnstile(_request("stale"))
    assert (exc.value.status_code, exc.value.detail) == (400, "TURNSTILE_FAILED")


@pytest.mark.parametrize("mode", ["down", "garbage"])
async def test_an_unreachable_cloudflare_is_a_refusal_not_a_pass(cloudflare, mode):
    state, _ = cloudflare
    state["mode"] = mode
    with pytest.raises(HTTPException) as exc:
        await turnstile.require_turnstile(_request("token"))
    assert (exc.value.status_code, exc.value.detail) == (503, "TURNSTILE_UNAVAILABLE")


async def test_the_secret_and_token_never_reach_the_log(cloudflare, caplog):
    state, _ = cloudflare
    state["mode"] = "fail"
    caplog.set_level("DEBUG")
    with pytest.raises(HTTPException):
        await turnstile.require_turnstile(_request("secret-looking-token"))
    assert "secret-looking-token" not in caplog.text
    assert "1x0000000000000000000000000000000AA" not in caplog.text
