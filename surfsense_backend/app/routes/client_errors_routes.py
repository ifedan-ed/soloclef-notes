"""Browser crash reports, so a "Something went wrong" page leaves a trace.

The web app's error boundaries and its window ``error`` /
``unhandledrejection`` hooks POST a small report here; it becomes one WARNING
line in the backend log (logger ``surfsense.client``) with the user id, the
page path and the build it came from.

Guard rails: signed-in browser sessions only (no PATs), a hard body cap, per-user and global rate
limits, and nothing is stored — the log line is the whole product. The report
carries no page content, query strings or user-entered text by design; the
message and stack are redacted and flattened like every other log line.
"""

from __future__ import annotations

import logging
import threading
import time
from collections import defaultdict, deque
from typing import Literal

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from pydantic import BaseModel, Field, ValidationError

from app.auth.context import AuthContext
from app.users import require_session_context

router = APIRouter()
client_logger = logging.getLogger("surfsense.client")

MAX_BODY_BYTES = 8 * 1024
PER_USER_LIMIT = 10  # reports per window per user
GLOBAL_LIMIT = 120  # reports per window across all users
WINDOW_SECONDS = 60.0
_STACK_LINES = 12


class ClientErrorReport(BaseModel):
    kind: Literal[
        "error-boundary", "global-error", "window-error", "unhandled-rejection"
    ]
    message: str = Field(default="", max_length=1000)
    name: str = Field(default="", max_length=100)
    stack: str = Field(default="", max_length=6000)
    path: str = Field(default="", max_length=500)
    digest: str = Field(default="", max_length=100)
    build: str = Field(default="", max_length=64)
    request_id: str = Field(default="", max_length=64)


class _SlidingWindow:
    def __init__(self) -> None:
        self._hits: dict[str, deque[float]] = defaultdict(deque)
        self._lock = threading.Lock()

    def allow(self, key: str, limit: int, window: float = WINDOW_SECONDS) -> bool:
        now = time.monotonic()
        with self._lock:
            hits = self._hits[key]
            while hits and now - hits[0] > window:
                hits.popleft()
            if len(hits) >= limit:
                return False
            hits.append(now)
            return True

    def reset(self) -> None:
        with self._lock:
            self._hits.clear()


_limiter = _SlidingWindow()


def _flat(text: str, limit: int) -> str:
    return " ".join(text.split())[:limit]


def _clean_path(path: str) -> str:
    # Pathname only: query strings and fragments can carry tokens.
    return _flat(path.split("?", 1)[0].split("#", 1)[0], 300) or "-"


def _stack_excerpt(stack: str) -> str:
    lines = [ln.strip() for ln in stack.splitlines() if ln.strip()][:_STACK_LINES]
    # Indented and prefixed so a crafted stack cannot pass for a log line.
    return "".join(f"\n    | {ln[:300]}" for ln in lines)


@router.post("/client-errors", status_code=204)
async def report_client_error(
    request: Request,
    auth: AuthContext = Depends(require_session_context),
) -> Response:
    declared = request.headers.get("content-length")
    if declared and declared.isdigit() and int(declared) > MAX_BODY_BYTES:
        raise HTTPException(status_code=413, detail="Report too large")
    body = await request.body()
    if len(body) > MAX_BODY_BYTES:
        raise HTTPException(status_code=413, detail="Report too large")

    user_id = str(auth.user.id)
    if not _limiter.allow(f"user:{user_id}", PER_USER_LIMIT) or not _limiter.allow(
        "global", GLOBAL_LIMIT
    ):
        raise HTTPException(status_code=429, detail="RATE_LIMIT_EXCEEDED")

    try:
        report = ClientErrorReport.model_validate_json(body)
    except ValidationError:
        raise HTTPException(status_code=422, detail="Invalid report") from None

    client_logger.warning(
        "client error: kind=%s path=%s build=%s%s%s %s: %s%s",
        report.kind,
        _clean_path(report.path),
        _flat(report.build, 64) or "-",
        f" digest={_flat(report.digest, 100)}" if report.digest else "",
        f" failed_request={_flat(report.request_id, 64)}" if report.request_id else "",
        _flat(report.name, 100) or "Error",
        _flat(report.message, 500) or "(no message)",
        _stack_excerpt(report.stack),
    )
    return Response(status_code=204)
