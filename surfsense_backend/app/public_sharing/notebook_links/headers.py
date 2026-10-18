"""Response headers for every ``/api/v1/public-notebooks/...`` request.

These routes read no cookie and no bearer token, so cross-origin access is
granted without credentials (``Access-Control-Allow-Origin: *``, never
``Allow-Credentials``): a page elsewhere may read a public notebook, as anyone
with the link can, but never with the visitor's session. The token is in the
URL, so no referrer is sent onward, and nothing here is for search engines.

Pure ASGI, added outside the app's CORS middleware so its credentialed answer
for the app's own origins is replaced on these paths, and preflights are
answered here.
"""

from __future__ import annotations

from starlette.types import ASGIApp, Message, Receive, Scope, Send

PREFIX = "/api/v1/public-notebooks/"

_ALWAYS = (
    (b"x-robots-tag", b"noindex, nofollow, noarchive"),
    (b"referrer-policy", b"no-referrer"),
    (b"x-content-type-options", b"nosniff"),
)
_CORS = (
    (b"access-control-allow-origin", b"*"),
    (
        b"access-control-expose-headers",
        b"Content-Disposition, Content-Length, Content-Range, Accept-Ranges, ETag",
    ),
)
_REPLACED = {name for name, _ in _ALWAYS + _CORS} | {
    b"access-control-allow-credentials",
    b"vary",
}


class PublicNotebookHeadersMiddleware:
    def __init__(self, app: ASGIApp) -> None:
        self.app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http" or not scope.get("path", "").startswith(PREFIX):
            await self.app(scope, receive, send)
            return

        if scope.get("method") == "OPTIONS":
            await send(
                {
                    "type": "http.response.start",
                    "status": 204,
                    "headers": [
                        *_ALWAYS,
                        *_CORS,
                        (b"access-control-allow-methods", b"GET, OPTIONS"),
                        (b"access-control-allow-headers", b"Range, If-None-Match"),
                        (b"access-control-max-age", b"86400"),
                    ],
                }
            )
            await send({"type": "http.response.body", "body": b""})
            return

        async def send_with_headers(message: Message) -> None:
            if message["type"] == "http.response.start":
                headers = [
                    (name, value)
                    for name, value in message.get("headers", [])
                    if name.lower() not in _REPLACED
                ]
                message = {**message, "headers": [*headers, *_ALWAYS, *_CORS]}
            await send(message)

        await self.app(scope, receive, send_with_headers)
