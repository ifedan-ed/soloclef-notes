#!/usr/bin/env python3
"""A PostHog stand-in: answers every capture and writes it down.

Both tiers send product events, and a tier whose analytics endpoint is
unreachable is not the tier the comparison is about: posthog-python retries and
logs, the Go queue retries and logs, and the two spend different amounts of
time doing it. So this answers 200 to everything on this host and nothing
leaves it.

What it records is readable at /captured and is not compared case by case
(parity/README.md, "What is not compared"): posthog-python flushes on a timer
from a background thread, so an event's arrival is not ordered against the
response that caused it.
"""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CAPTURED: list[dict] = []


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def answer(self, status: int, payload: object) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler's spelling
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        try:
            sent = json.loads(raw or b"{}")
        except ValueError:
            sent = {"undecodable": raw.decode("latin-1")}
        CAPTURED.append({"path": self.path, "sent": sent})
        self.answer(200, {"status": 1})

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler's spelling
        if self.path == "/captured":
            self.answer(200, CAPTURED)
            return
        # The feature-flag and decide endpoints a client may call on startup.
        self.answer(200, {"featureFlags": {}, "status": 1})

    def log_message(self, *_args) -> None:
        return


def main() -> int:
    port = int(os.environ["PARITY_ANALYTICS_PORT"])
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
