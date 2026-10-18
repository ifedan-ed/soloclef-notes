"""Where a finished sign-in lands: a page of Notes, never another site."""

from __future__ import annotations

DEFAULT_RETURN_PATH = "/dashboard"


def safe_return_path(requested: str | None) -> str:
    """``requested`` if it is a path on this site, else the dashboard.

    The login page passes the page someone was sent away from (a shared
    notebook link, say). Anything that a browser could read as another
    origin (``//evil``, ``/\\evil``, ``https:...``) or that is not a plain
    path falls back to the dashboard.
    """
    if not requested or len(requested) > 2048:
        return DEFAULT_RETURN_PATH
    if not requested.startswith("/") or requested.startswith(("//", "/\\")):
        return DEFAULT_RETURN_PATH
    if (
        any(ch in requested for ch in ("\\", "\r", "\n", "\t"))
        or ":" in requested.split("?", 1)[0]
    ):
        return DEFAULT_RETURN_PATH
    if requested.startswith(("/login", "/auth/")):
        return DEFAULT_RETURN_PATH
    return requested
