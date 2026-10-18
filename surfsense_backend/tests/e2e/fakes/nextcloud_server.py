"""A stand-in Nextcloud for the web e2e stack: Login Flow v2, OCS user, app passwords, WebDAV.

Run as its own service (docker-compose.e2e-web.yml). The backend reaches it
at ``http://nextcloud-stub:8080``; the browser reaches its login pages
through the e2e proxy at ``/__nextcloud__/``, so the login URL it hands out
uses ``NEXTCLOUD_STUB_PUBLIC_URL``. Standard library only; state is in memory.

    GET  /login/v2/flow/<id>         the sign-in page, with a "Grant access" button
    POST /login/v2/flow/<id>/grant   grants it (what the person does in Nextcloud)
    GET  /__stub__/state             revoked and live app passwords, for assertions
"""

from __future__ import annotations

import base64
import html
import json
import os
import secrets
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, unquote, urlsplit

INTERNAL = os.environ.get("NEXTCLOUD_STUB_INTERNAL_URL", "http://nextcloud-stub:8080")
PUBLIC = os.environ.get(
    "NEXTCLOUD_STUB_PUBLIC_URL", "http://127.0.0.1:3940/__nextcloud__"
).rstrip("/")
PREFIX = "/__nextcloud__"
LOGIN_NAME = "e2e-user"
USER_ID = "e2e-user"
DISPLAY_NAME = "E2E Nextcloud User"

_lock = threading.Lock()
_flows: dict[str, dict] = {}  # login id -> {"poll": token, "password": str | None}
_passwords: set[str] = set()
_revoked: list[str] = []

_ROOT = f"/remote.php/dav/files/{USER_ID}"
_LISTING = (
    '<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">'
    f"<d:response><d:href>{_ROOT}/</d:href><d:propstat><d:prop>"
    "<d:resourcetype><d:collection/></d:resourcetype><d:getetag>&quot;root&quot;</d:getetag>"
    "</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>"
    f"<d:response><d:href>{_ROOT}/Welcome.md</d:href><d:propstat><d:prop>"
    "<d:resourcetype/><d:getcontentlength>20</d:getcontentlength>"
    "<d:getcontenttype>text/markdown</d:getcontenttype>"
    "<d:getlastmodified>Tue, 01 Jul 2025 10:00:00 GMT</d:getlastmodified>"
    "<d:getetag>&quot;w1&quot;</d:getetag></d:prop>"
    "<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>"
)


class Handler(BaseHTTPRequestHandler):
    server_version = "NextcloudStub"

    def _path(self) -> str:
        path = unquote(urlsplit(self.path).path)
        return path[len(PREFIX) :] if path.startswith(PREFIX) else path

    def _body(self) -> bytes:
        length = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(length) if length else b""

    def _send(self, status: int, body: bytes | str = b"", ctype="application/json"):
        data = body.encode() if isinstance(body, str) else body
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _password(self) -> str | None:
        header = self.headers.get("Authorization", "")
        if not header.startswith("Basic "):
            return None
        user, _, password = base64.b64decode(header[6:]).decode().partition(":")
        with _lock:
            ok = user == LOGIN_NAME and password in _passwords
        return password if ok else None

    def log_message(self, *args):  # quiet
        return

    def do_GET(self):
        path = self._path()
        if path.startswith("/login/v2/flow/"):
            login_id = path.rsplit("/", 1)[-1]
            if login_id not in _flows:
                return self._send(404, "unknown login", "text/plain")
            page = (
                "<!doctype html><title>Nextcloud stub</title>"
                "<h1>Connect to your account</h1>"
                "<p>Soloclef Notes wants access to your Nextcloud account.</p>"
                f'<form method="post" action="{html.escape(login_id)}/grant">'
                "<button type='submit'>Grant access</button></form>"
            )
            return self._send(200, page, "text/html; charset=utf-8")
        if path == "/ocs/v2.php/cloud/user":
            if self._password() is None:
                return self._send(401, "{}")
            return self._send(
                200,
                json.dumps(
                    {"ocs": {"data": {"id": USER_ID, "display-name": DISPLAY_NAME}}}
                ),
            )
        if path == "/__stub__/state":
            with _lock:
                state = {"revoked": len(_revoked), "live": len(_passwords)}
            return self._send(200, json.dumps(state))
        return self._send(404, "{}")

    def do_POST(self):
        path = self._path()
        if path == "/index.php/login/v2":
            login_id, poll = secrets.token_urlsafe(12), secrets.token_urlsafe(24)
            with _lock:
                _flows[login_id] = {"poll": poll, "password": None}
            return self._send(
                200,
                json.dumps(
                    {
                        "poll": {
                            "token": poll,
                            "endpoint": f"{INTERNAL}/login/v2/poll",
                        },
                        "login": f"{PUBLIC}/login/v2/flow/{login_id}",
                    }
                ),
            )
        if path.startswith("/login/v2/flow/") and path.endswith("/grant"):
            login_id = path.split("/")[-2]
            with _lock:
                flow = _flows.get(login_id)
                if flow is None:
                    return self._send(404, "unknown login", "text/plain")
                flow["password"] = secrets.token_urlsafe(32)
                _passwords.add(flow["password"])
            return self._send(
                200,
                "<!doctype html><h1>Account connected</h1><p>You can close this window.</p>",
                "text/html; charset=utf-8",
            )
        if path == "/login/v2/poll":
            token = parse_qs(self._body().decode()).get("token", [""])[0]
            with _lock:
                match = next(
                    (
                        (k, f)
                        for k, f in _flows.items()
                        if f["poll"] == token and f["password"]
                    ),
                    None,
                )
                if match is None:
                    return self._send(404, "[]")
                del _flows[match[0]]
            return self._send(
                200,
                json.dumps(
                    {
                        "server": INTERNAL,
                        "loginName": LOGIN_NAME,
                        "appPassword": match[1]["password"],
                    }
                ),
            )
        return self._send(404, "{}")

    def do_DELETE(self):
        if self._path() == "/ocs/v2.php/core/apppassword":
            password = self._password()
            if password is None:
                return self._send(401, "{}")
            with _lock:
                _passwords.discard(password)
                _revoked.append(password)
            return self._send(200, json.dumps({"ocs": {"data": []}}))
        return self._send(404, "{}")

    def do_PROPFIND(self):
        path = self._path()
        self._body()
        if not path.startswith(_ROOT):
            return self._send(404, "")
        if self._password() is None:
            return self._send(401, "")
        if path.rstrip("/") == _ROOT:
            return self._send(207, _LISTING, "application/xml; charset=utf-8")
        return self._send(404, "")


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
