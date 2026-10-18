"""Request context + access log, as one pure-ASGI middleware.

* Accepts a well-formed ``X-Request-ID`` from the caller or mints ``req_<12 hex>``,
  exposes it on ``request.state.request_id`` and echoes it in the response.
* Installs a fresh log context for the request (request id, workspace id parsed
  from the path; the auth dependency adds the user id).
* Writes one access line per request with status and duration, in place of
  uvicorn's (which had neither the id nor the time). Health probes that
  succeed are not logged; ``?token=``-style query values are redacted by the
  formatter.

Pure ASGI rather than ``BaseHTTPMiddleware`` so streaming responses (chat SSE)
pass through untouched and the context is the one the endpoint runs in.
"""

from __future__ import annotations

import logging
import re
import time
import uuid
from typing import Any

from .context import _LOG_CONTEXT, bind_log_context

access_logger = logging.getLogger("surfsense.access")

REQUEST_ID_HEADER = "X-Request-ID"
_VALID_REQUEST_ID = re.compile(r"^[A-Za-z0-9._:-]{6,64}$")
_WORKSPACE_IN_PATH = re.compile(r"/workspaces/(\d+)(?:/|$)")
_QUIET_PATHS = frozenset({"/ready", "/health", "/healthz", "/api/v1/health"})


def new_request_id() -> str:
    return f"req_{uuid.uuid4().hex[:12]}"


def accept_request_id(raw: str | None) -> str:
    """Use the caller's id only if it is short and plain; mint one otherwise."""
    if raw and _VALID_REQUEST_ID.match(raw):
        return raw
    return new_request_id()


class RequestContextMiddleware:
    def __init__(self, app: Any) -> None:
        self.app = app

    async def __call__(self, scope: dict, receive: Any, send: Any) -> None:
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return

        raw = None
        for key, value in scope.get("headers") or ():
            if key == b"x-request-id":
                raw = value.decode("latin-1")
                break
        request_id = accept_request_id(raw)
        scope.setdefault("state", {})["request_id"] = request_id

        path = scope.get("path", "")
        token = _LOG_CONTEXT.set({})
        bind_log_context(request_id=request_id)
        ws = _WORKSPACE_IN_PATH.search(path)
        if ws:
            bind_log_context(workspace_id=ws.group(1))

        status_holder = {"status": 500}
        started = time.perf_counter()

        async def send_with_id(message: dict) -> None:
            if message.get("type") == "http.response.start":
                status_holder["status"] = int(message.get("status", 500))
                headers = [
                    (k, v)
                    for k, v in message.get("headers", [])
                    if k.lower() != b"x-request-id"
                ]
                headers.append((b"x-request-id", request_id.encode("latin-1")))
                message["headers"] = headers
            await send(message)

        try:
            await self.app(scope, receive, send_with_id)
        finally:
            status = status_holder["status"]
            elapsed_ms = (time.perf_counter() - started) * 1000
            if not (path in _QUIET_PATHS and status < 400):
                query = scope.get("query_string") or b""
                target = path + ("?" + query.decode("latin-1") if query else "")
                # The error handlers already log 4xx/5xx details at
                # WARNING/ERROR; the access line only flags what they do not
                # see (5xx from outside a route, SlowAPI 429s).
                level = (
                    logging.WARNING if status >= 500 or status == 429 else logging.INFO
                )
                access_logger.log(
                    level,
                    "%s %s %d %.0fms",
                    scope.get("method", "-"),
                    target[:500],
                    status,
                    elapsed_ms,
                )
            _LOG_CONTEXT.reset(token)
