"""Which shared files a browser may show inline, and the headers that keep the rest inert.

A public file is served from the app's own origin, so anything a browser would
execute (HTML, SVG, scripts) is sent as a sandboxed download instead.
"""

from __future__ import annotations

from typing import Literal

Display = Literal["image", "pdf", "audio", "video", "file"]

_INLINE_IMAGES = {"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"}


def display_for(mime_type: str | None) -> Display:
    mime = (mime_type or "").split(";")[0].strip().lower()
    if mime in _INLINE_IMAGES:
        return "image"
    if mime == "application/pdf":
        return "pdf"
    if mime.startswith("audio/"):
        return "audio"
    if mime.startswith("video/"):
        return "video"
    return "file"


def public_file_headers(mime_type: str | None, filename: str | None) -> dict[str, str]:
    headers = {
        "Cache-Control": "public, max-age=86400",
        "X-Content-Type-Options": "nosniff",
    }
    if display_for(mime_type) == "file":
        safe = (filename or "download").replace('"', "").replace("\n", " ")
        headers["Content-Disposition"] = f'attachment; filename="{safe}"'
        headers["Content-Security-Policy"] = "sandbox"
    return headers
