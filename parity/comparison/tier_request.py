"""Send one request to one tier and read back exactly what it answered.

It is deliberately not a convenience client. The comparison is about bytes, so
nothing here may add a header the case did not ask for, re-encode a body,
follow a redirect, decompress anything, or fold two response headers of the
same name into one — that last being how a doubled CORS header hides
(parity/cases/regressions.json).
"""

from __future__ import annotations

import http.client
import urllib.parse

# The response headers a client reads, and therefore the ones a handover may not
# change: the content and framing headers, and the whole credentialed CORS
# policy.
HEADERS_COMPARED = (
    "content-type",
    "content-length",
    "transfer-encoding",
    "content-disposition",
    "cache-control",
    "allow",
    "access-control-allow-origin",
    "access-control-allow-credentials",
    "access-control-allow-methods",
    "access-control-allow-headers",
    "access-control-expose-headers",
    "access-control-max-age",
    "vary",
)


class Answer:
    """What one tier said: the status, the body's bytes, and the headers read."""

    def __init__(self, status: int, body: bytes, headers: list[tuple[str, str]]):
        self.status = status
        self.body = body
        # A list and not a mapping, in order: two headers of the same name are
        # the divergence, not a detail to tidy away.
        self.headers = headers


def ask(base: str, request: dict) -> Answer:
    target = urllib.parse.urlsplit(base)
    connection = http.client.HTTPConnection(target.hostname, target.port, timeout=120)
    try:
        body = request.get("body")
        payload = body.encode("utf-8") if isinstance(body, str) else body
        connection.putrequest(
            request.get("method", "GET"),
            request["path"],
            skip_accept_encoding=True,
        )
        for name, value in request.get("headers", {}).items():
            connection.putheader(name, value)
        if payload is not None:
            connection.putheader("Content-Length", str(len(payload)))
        connection.endheaders(payload)

        response = connection.getresponse()
        read = response.read()
        headers = [
            (name.lower(), value)
            for name, value in response.getheaders()
            if name.lower() in HEADERS_COMPARED
        ]
        return Answer(response.status, read, headers)
    finally:
        connection.close()
