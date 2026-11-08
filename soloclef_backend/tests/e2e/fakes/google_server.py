"""A stand-in Google for tests: OAuth consent, token, revoke, and the Drive v3 calls we make.

Runs as its own service in the web e2e stack (docker-compose.e2e-web.yml) and
in-process for backend integration tests (``serve_in_thread``). The backend
reaches it directly (``GOOGLE_STUB_INTERNAL_URL``); the browser reaches the
consent page through the e2e proxy at ``/__google__/`` (``GOOGLE_STUB_PUBLIC_URL``).
Standard library only; state is in memory.

    GET  /o/oauth2/auth              the consent page, "Allow" / "Cancel"
    POST /o/oauth2/decide            what the person clicks; redirects back with a code
    POST /token                      authorization_code (PKCE checked) and refresh_token
    POST /revoke                     revokes a refresh or access token
    GET  /drive/v3/about             the account (emailAddress)
    GET  /drive/v3/files/<id>        metadata; ``alt=media`` downloads
    GET  /drive/v3/files/<id>/export exports a Google Docs file
    GET  /drive/v3/changes/startPageToken, /drive/v3/changes
    GET  /__stub__/state             tokens issued, refreshed and revoked, for assertions
    POST /__stub__/files/<id>        replace a file's content (a change in Drive)
    POST /__stub__/reset             forget every token and edit
"""

from __future__ import annotations

import base64
import hashlib
import html
import json
import os
import secrets
import threading
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, unquote, urlencode, urlsplit

PREFIX = "/__google__"
ACCOUNT_EMAIL = "e2e.drive@example.com"
ACCOUNT_NAME = "E2E Drive User"
GOOGLE_DOC = "application/vnd.google-apps.document"
DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"


@dataclass
class StubFile:
    id: str
    name: str
    mime_type: str
    content: bytes
    modified: str = "2026-09-01T10:00:00.000Z"
    #: What an export returns, by export MIME type (Google Docs files only).
    exports: dict[str, bytes] = field(default_factory=dict)

    def metadata(self) -> dict:
        meta = {
            "id": self.id,
            "name": self.name,
            "mimeType": self.mime_type,
            "modifiedTime": self.modified,
            "createdTime": "2026-08-01T10:00:00.000Z",
            "parents": ["root"],
            "webViewLink": f"https://drive.google.com/file/d/{self.id}/view",
        }
        if self.mime_type != GOOGLE_DOC:
            meta["md5Checksum"] = hashlib.md5(self.content).hexdigest()
            meta["size"] = str(len(self.content))
        return meta


def default_files() -> dict[str, StubFile]:
    notes = StubFile(
        id="e2eDriveNotes0001",
        name="Drive field notes.md",
        mime_type="text/markdown",
        content=b"# Drive field notes\n\nThe canary word is PERIWINKLE-DRIVE.\n",
    )
    plan = StubFile(
        id="e2eDrivePlan00002",
        name="Research plan.txt",
        mime_type="text/plain",
        content=b"Research plan: compare three cohorts. Canary AMBER-DRIVE.\n",
    )
    doc = StubFile(
        id="e2eDriveGdoc00003",
        name="Shared Google Doc",
        mime_type=GOOGLE_DOC,
        content=b"",
    )
    return {f.id: f for f in (notes, plan, doc)}


@dataclass
class StubState:
    public_url: str
    files: dict[str, StubFile] = field(default_factory=default_files)
    codes: dict[str, dict] = field(default_factory=dict)
    refresh_tokens: set[str] = field(default_factory=set)
    access_tokens: set[str] = field(default_factory=set)
    revoked: list[str] = field(default_factory=list)
    token_requests: list[str] = field(default_factory=list)
    drive_requests: list[str] = field(default_factory=list)
    lock: threading.Lock = field(default_factory=threading.Lock)

    def snapshot(self) -> dict:
        with self.lock:
            return {
                "live_refresh_tokens": len(self.refresh_tokens),
                "live_access_tokens": len(self.access_tokens),
                "revoked": list(self.revoked),
                "token_requests": list(self.token_requests),
                "drive_requests": list(self.drive_requests),
            }


def _pkce_matches(challenge: str | None, method: str | None, verifier: str) -> bool:
    if not challenge:
        return True
    if (method or "plain").upper() == "S256":
        digest = hashlib.sha256(verifier.encode()).digest()
        return base64.urlsafe_b64encode(digest).rstrip(b"=").decode() == challenge
    return challenge == verifier


def make_handler(state: StubState) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        server_version = "GoogleStub"

        def log_message(self, *_args) -> None:
            return

        # -- plumbing ---------------------------------------------------
        def _split(self) -> tuple[str, dict[str, str]]:
            parts = urlsplit(self.path)
            path = parts.path
            if path.startswith(PREFIX):
                path = path[len(PREFIX) :] or "/"
            query = {k: v[0] for k, v in parse_qs(parts.query).items()}
            return path, query

        def _form(self) -> dict[str, str]:
            length = int(self.headers.get("Content-Length") or 0)
            raw = self.rfile.read(length).decode() if length else ""
            return {k: v[0] for k, v in parse_qs(raw).items()}

        def _send(self, status: int, body: bytes, content_type: str) -> None:
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _json(self, status: int, payload: dict) -> None:
            self._send(status, json.dumps(payload).encode(), "application/json")

        def _redirect(self, location: str) -> None:
            self.send_response(302)
            self.send_header("Location", location)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def _bearer_ok(self) -> bool:
            auth = self.headers.get("Authorization") or ""
            token = auth[7:] if auth.startswith("Bearer ") else ""
            with state.lock:
                return token in state.access_tokens

        def _unauthorized(self) -> None:
            self._json(
                401,
                {
                    "error": {
                        "code": 401,
                        "message": "Request had invalid authentication credentials.",
                        "status": "UNAUTHENTICATED",
                    }
                },
            )

        # -- routes -----------------------------------------------------
        def do_GET(self) -> None:
            path, query = self._split()
            if path == "/o/oauth2/auth":
                return self._consent_page(query)
            if path == "/__stub__/state":
                return self._json(200, state.snapshot())
            if path.startswith("/drive/v3/"):
                return self._drive(path[len("/drive/v3/") :], query)
            self._json(404, {"error": "not found", "path": path})

        def do_POST(self) -> None:
            path, query = self._split()
            if path == "/o/oauth2/decide":
                return self._decide(self._form())
            if path == "/token":
                return self._token(self._form())
            if path in ("/revoke", "/o/oauth2/revoke"):
                return self._revoke({**query, **self._form()})
            if path == "/__stub__/reset":
                with state.lock:
                    state.files = default_files()
                    state.codes.clear()
                    state.refresh_tokens.clear()
                    state.access_tokens.clear()
                    state.revoked.clear()
                    state.token_requests.clear()
                    state.drive_requests.clear()
                return self._json(200, {"ok": True})
            if path.startswith("/__stub__/files/"):
                return self._edit_file(path.rsplit("/", 1)[-1])
            self._json(404, {"error": "not found", "path": path})

        # -- OAuth ------------------------------------------------------
        def _consent_page(self, query: dict[str, str]) -> None:
            fields = "".join(
                f'<input type="hidden" name="{html.escape(k)}" value="{html.escape(v)}">'
                for k, v in query.items()
            )
            action = f"{state.public_url}/o/oauth2/decide"
            scope = html.escape(query.get("scope", ""))
            page = (
                "<!doctype html><title>Sign in - Google Accounts (stub)</title>"
                f"<h1>Soloclef Notes wants access to your Google Account</h1>"
                f"<p>{ACCOUNT_EMAIL}</p><p data-testid='scopes'>{scope}</p>"
                f'<form method="post" action="{action}">{fields}'
                '<button name="decision" value="allow">Allow</button> '
                '<button name="decision" value="deny">Cancel</button></form>'
            )
            self._send(200, page.encode(), "text/html; charset=utf-8")

        def _decide(self, form: dict[str, str]) -> None:
            redirect_uri = form.get("redirect_uri", "")
            params: dict[str, str] = {}
            if form.get("state"):
                params["state"] = form["state"]
            if form.get("decision") != "allow":
                params["error"] = "access_denied"
            else:
                code = "4/stub-" + secrets.token_urlsafe(12)
                with state.lock:
                    state.codes[code] = {
                        "client_id": form.get("client_id"),
                        "redirect_uri": redirect_uri,
                        "challenge": form.get("code_challenge"),
                        "method": form.get("code_challenge_method"),
                        "scope": form.get("scope", ""),
                    }
                params["code"] = code
                params["scope"] = form.get("scope", "")
            sep = "&" if "?" in redirect_uri else "?"
            self._redirect(f"{redirect_uri}{sep}{urlencode(params)}")

        def _issue_access(self) -> str:
            token = "ya29.stub-" + secrets.token_urlsafe(16)
            state.access_tokens.add(token)
            return token

        def _basic_client_id(self) -> str | None:
            auth = self.headers.get("Authorization") or ""
            if not auth.startswith("Basic "):
                return None
            try:
                decoded = base64.b64decode(auth[6:]).decode()
            except ValueError:
                return None
            return unquote(decoded.split(":", 1)[0])

        def _token(self, form: dict[str, str]) -> None:
            grant = form.get("grant_type")
            # Clients send their id in the form or with HTTP Basic auth.
            form = {"client_id": self._basic_client_id() or "", **form}
            with state.lock:
                state.token_requests.append(grant or "")
                if grant == "authorization_code":
                    issued = state.codes.pop(form.get("code", ""), None)
                    if (
                        issued is None
                        or issued["redirect_uri"] != form.get("redirect_uri")
                        or issued["client_id"] != form.get("client_id")
                        or not _pkce_matches(
                            issued["challenge"],
                            issued["method"],
                            form.get("code_verifier", ""),
                        )
                    ):
                        return self._json(400, {"error": "invalid_grant"})
                    refresh = "1//stub-" + secrets.token_urlsafe(16)
                    state.refresh_tokens.add(refresh)
                    return self._json(
                        200,
                        {
                            "access_token": self._issue_access(),
                            "refresh_token": refresh,
                            "expires_in": 3599,
                            "scope": issued["scope"],
                            "token_type": "Bearer",
                        },
                    )
                if grant == "refresh_token":
                    if form.get("refresh_token") not in state.refresh_tokens:
                        return self._json(
                            400,
                            {
                                "error": "invalid_grant",
                                "error_description": "Token has been expired or revoked.",
                            },
                        )
                    return self._json(
                        200,
                        {
                            "access_token": self._issue_access(),
                            "expires_in": 3599,
                            "scope": "https://www.googleapis.com/auth/drive.file",
                            "token_type": "Bearer",
                        },
                    )
            self._json(400, {"error": "unsupported_grant_type"})

        def _revoke(self, form: dict[str, str]) -> None:
            token = form.get("token", "")
            with state.lock:
                known = token in state.refresh_tokens or token in state.access_tokens
                state.refresh_tokens.discard(token)
                state.access_tokens.discard(token)
                if known:
                    state.revoked.append(token)
            if known:
                return self._json(200, {})
            self._json(400, {"error": "invalid_token"})

        # -- Drive ------------------------------------------------------
        def _drive(self, rest: str, query: dict[str, str]) -> None:
            with state.lock:
                state.drive_requests.append(
                    rest + ("?alt=media" if query.get("alt") == "media" else "")
                )
            if not self._bearer_ok():
                return self._unauthorized()
            if rest == "about":
                return self._json(
                    200,
                    {
                        "user": {
                            "emailAddress": ACCOUNT_EMAIL,
                            "displayName": ACCOUNT_NAME,
                        }
                    },
                )
            if rest == "changes/startPageToken":
                return self._json(200, {"startPageToken": "100"})
            if rest == "changes":
                return self._json(
                    200,
                    {"changes": [], "newStartPageToken": query.get("pageToken", "100")},
                )
            if rest == "files":
                with state.lock:
                    files = [f.metadata() for f in state.files.values()]
                return self._json(200, {"files": files})
            parts = rest.split("/")
            if len(parts) >= 2 and parts[0] == "files":
                with state.lock:
                    stub = state.files.get(parts[1])
                if stub is None:
                    return self._json(
                        404,
                        {
                            "error": {
                                "code": 404,
                                "message": f"File not found: {parts[1]}.",
                            }
                        },
                    )
                if len(parts) == 3 and parts[2] == "export":
                    body = stub.exports.get(query.get("mimeType", ""))
                    if body is None:
                        return self._json(400, {"error": {"code": 400}})
                    return self._send(200, body, query["mimeType"])
                if query.get("alt") == "media":
                    return self._send(200, stub.content, stub.mime_type)
                return self._json(200, stub.metadata())
            self._json(404, {"error": {"code": 404, "message": rest}})

        def _edit_file(self, file_id: str) -> None:
            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length) if length else b""
            with state.lock:
                stub = state.files.get(file_id)
                if stub is None:
                    return self._json(404, {"error": "no such file"})
                stub.content = body
                stub.modified = "2026-09-20T12:00:00.000Z"
            self._json(200, stub.metadata())

    return Handler


def serve_in_thread(
    public_url: str | None = None,
) -> tuple[ThreadingHTTPServer, StubState]:
    """Start on a free loopback port; ``state.public_url`` is where it listens."""
    server = ThreadingHTTPServer(("127.0.0.1", 0), BaseHTTPRequestHandler)
    base = public_url or f"http://127.0.0.1:{server.server_address[1]}"
    state = StubState(public_url=base)
    server.RequestHandlerClass = make_handler(state)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, state


def main() -> None:
    public = os.environ.get(
        "GOOGLE_STUB_PUBLIC_URL", "http://127.0.0.1:3940/__google__"
    ).rstrip("/")
    state = StubState(public_url=public)
    port = int(os.environ.get("GOOGLE_STUB_PORT", "8080"))
    ThreadingHTTPServer(("0.0.0.0", port), make_handler(state)).serve_forever()


if __name__ == "__main__":
    main()
