"""The HTTP surface: ``POST /v1/parse`` and ``GET /health``.

``POST /v1/parse?filename=<name>`` takes the file as the raw request body
(not multipart: there is one file and nothing else to send, and a raw body
can be copied to disk as it arrives) and answers

    200  {"parser", "parser_version", "pages": [{"number", "markdown"}]}

or ``{"error": {"kind", "message"}}`` with:

    415 unsupported_format   the extension is not one the parser reads
    422 no_text_layer        a scan: no page has text
    422 parse_error          the parser raised, died or timed out on this file
    413 too_large            the body is over PARSE_SERVICE_MAX_BYTES
    411 length_required      no Content-Length (the size is checked before reading)
    400 bad_request          no filename
    503 busy                 every worker is occupied; nothing was started, retry
    404 not_found

The first three are refusals of the file and will not change on a retry.

The standard library's server is used on purpose. It is slow next to an ASGI
server and that does not matter: a request here is seconds of CPU in a worker
process, and every dependency left out is one less thing in the only Python
image the Go pipeline carries. It is an internal service: do not publish its
port.
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

from parse_service.convert import (
    PARSER_NAME,
    SUPPORTED_EXTENSIONS,
    extension_of,
    parser_version,
)
from parse_service.refusal import NO_TEXT_LAYER, PARSE_ERROR, UNSUPPORTED_FORMAT
from parse_service.workers import Busy, WorkerPool

_COPY_CHUNK = 1024 * 1024
_REFUSAL_STATUS = {UNSUPPORTED_FORMAT: 415, NO_TEXT_LAYER: 422, PARSE_ERROR: 422}
#: A client that stops sending mid-body must not hold a thread forever.
_SOCKET_TIMEOUT_SECONDS = 120


class _Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    timeout = _SOCKET_TIMEOUT_SECONDS
    server: "ParseServer"

    def log_request(self, code="-", size="-") -> None:
        # The default line prints the whole request line. The filename in the
        # query is user data and stays out of the logs.
        print(
            f"{self.command} {urlsplit(self.path).path} {code}",
            file=sys.stderr,
            flush=True,
        )

    def _json(self, status: int, body: dict, headers: dict | None = None) -> None:
        payload = json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(payload)))
        for name, value in (headers or {}).items():
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(payload)

    def _error(self, status: int, kind: str, message: str, **headers: str) -> None:
        # The body may be unread; closing the connection is the only safe way
        # to leave it that way under HTTP/1.1.
        self.close_connection = True
        self._json(
            status,
            {"error": {"kind": kind, "message": message}},
            {"Connection": "close", **headers},
        )

    def do_GET(self) -> None:  # noqa: N802
        if urlsplit(self.path).path != "/health":
            self._error(404, "not_found", "No such path.")
            return
        pool = self.server.pool
        alive = pool.alive()
        self._json(
            200 if alive == pool.size else 503,
            {
                "status": "ok" if alive == pool.size else "degraded",
                "parser": PARSER_NAME,
                "parser_version": self.server.parser_version,
                "workers": pool.size,
                "workers_alive": alive,
                "workers_idle": pool.idle(),
            },
        )

    def do_POST(self) -> None:  # noqa: N802
        url = urlsplit(self.path)
        if url.path != "/v1/parse":
            self._error(404, "not_found", "No such path.")
            return
        filename = (parse_qs(url.query).get("filename") or [""])[0]
        if not filename:
            self._error(400, "bad_request", "The filename query parameter is required.")
            return
        extension = extension_of(filename)
        if extension not in SUPPORTED_EXTENSIONS:
            self._error(
                415,
                UNSUPPORTED_FORMAT,
                f"{extension or 'A file with no extension'} is not read here. "
                f"Supported: {', '.join(sorted(SUPPORTED_EXTENSIONS))}.",
            )
            return
        length = self.headers.get("Content-Length")
        if length is None or not length.isdigit():
            self._error(411, "length_required", "Content-Length is required.")
            return
        size = int(length)
        if size > self.server.max_bytes:
            self._error(
                413,
                "too_large",
                f"The file is {size} bytes; the limit is {self.server.max_bytes}.",
            )
            return

        # MuPDF picks its reader from the path's extension, so the temporary
        # file keeps it. Nothing else of the name is used.
        handle, path = tempfile.mkstemp(suffix=extension)
        try:
            with os.fdopen(handle, "wb") as spool:
                remaining = size
                while remaining:
                    chunk = self.rfile.read(min(_COPY_CHUNK, remaining))
                    if not chunk:
                        # The client went away mid-body; nobody to answer.
                        self.close_connection = True
                        return
                    spool.write(chunk)
                    remaining -= len(chunk)
            try:
                result = self.server.pool.parse(path)
            except Busy:
                self._error(
                    503,
                    "busy",
                    "Every parser worker is occupied. Nothing was started.",
                    **{"Retry-After": "5"},
                )
                return
        finally:
            os.unlink(path)

        if "refusal" in result:
            kind = result["refusal"]["kind"]
            self._json(_REFUSAL_STATUS[kind], {"error": result["refusal"]})
            return
        self._json(
            200,
            {
                "parser": PARSER_NAME,
                "parser_version": self.server.parser_version,
                "pages": result["pages"],
            },
        )


class ParseServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address: tuple[str, int], pool: WorkerPool, max_bytes: int):
        super().__init__(address, _Handler)
        self.pool = pool
        self.max_bytes = max_bytes
        self.parser_version = parser_version()
