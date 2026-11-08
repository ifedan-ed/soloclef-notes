"""A stand-in for Microsoft's token endpoint (login.microsoftonline.com).

Answers code redemptions and refreshes the way the Microsoft identity
platform does, as an ``httpx.MockTransport``. Unsigned id_tokens are fine:
the code that reads them gets them straight from the token endpoint over TLS.
"""

from __future__ import annotations

import base64
import hashlib
import json
from dataclasses import dataclass, field
from urllib.parse import parse_qs

import httpx

from app.connectors.email_imap.microsoft.settings import IMAP_SCOPE

CLIENT_ID = "11111111-2222-3333-4444-555555555555"
CLIENT_SECRET = "a-client-secret-value"
MAILBOX = "someone@contoso.example"


def s256(verifier: str) -> str:
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def unsigned_id_token(claims: dict) -> str:
    def part(data: dict) -> str:
        raw = json.dumps(data).encode()
        return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()

    return f"{part({'alg': 'none', 'typ': 'JWT'})}.{part(claims)}."


def _always_sent() -> dict:
    return {
        "aud": CLIENT_ID,
        "iss": "https://login.microsoftonline.com/tid-1/v2.0",
        "sub": "subject-1",
        "tid": "tid-1",
    }


def id_token_for(address: str = MAILBOX, **extra) -> str:
    """A fixed id_token (as with the ``email`` scope only), used as is."""
    return unsigned_id_token({**_always_sent(), "email": address, **extra})


@dataclass
class SignedInAccount:
    """The Microsoft account on the consent page, as Microsoft holds it."""

    email: str | None = MAILBOX
    preferred_username: str = MAILBOX
    oid: str = "oid-1"
    name: str = "Someone"

    def id_token(self, scopes: set[str]) -> str:
        """Only the claims the requested scopes bring, as the Microsoft
        identity platform does: ``email`` needs the email scope;
        ``preferred_username``, ``oid`` and ``name`` need the profile scope
        (learn.microsoft.com/entra/identity-platform/id-token-claims-reference)."""
        claims = _always_sent()
        if "email" in scopes and self.email:
            claims["email"] = self.email
        if "profile" in scopes:
            claims["preferred_username"] = self.preferred_username
            claims["oid"] = self.oid
            claims["name"] = self.name
        return unsigned_id_token(claims)


@dataclass
class FakeMicrosoftLogin:
    """Codes it accepts, refresh tokens it accepts, and what it was sent.

    ``codes`` maps each code to the PKCE ``code_challenge`` it was issued
    for; redeeming it needs the verifier whose S256 hash is that challenge.
    """

    codes: dict[str, str] = field(
        default_factory=lambda: {"good-code": s256("verifier")}
    )
    refresh_tokens: set[str] = field(default_factory=lambda: {"refresh-1"})
    granted_scope: str = IMAP_SCOPE
    issue_refresh_token: bool = True
    account: SignedInAccount = field(default_factory=SignedInAccount)
    #: Sent as is instead of ``account``'s, when set.
    id_token: str | None = None
    #: Next answer overrides: (status, json body); used once each.
    scripted: list[tuple[int, dict]] = field(default_factory=list)
    requests: list[dict[str, str]] = field(default_factory=list)
    issued: int = 0

    @property
    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._handle)

    def _handle(self, request: httpx.Request) -> httpx.Response:
        assert request.url.path.endswith("/oauth2/v2.0/token")
        form = {k: v[0] for k, v in parse_qs(request.content.decode()).items()}
        self.requests.append(form)
        if self.scripted:
            status, body = self.scripted.pop(0)
            return httpx.Response(status, json=body)
        if (form.get("client_id"), form.get("client_secret")) != (
            CLIENT_ID,
            CLIENT_SECRET,
        ):
            return _error(401, "invalid_client", "AADSTS7000215")
        grant = form.get("grant_type")
        if grant == "authorization_code":
            challenge = self.codes.pop(form.get("code", ""), None)
            if challenge is None or challenge != s256(form.get("code_verifier", "")):
                return _error(400, "invalid_grant", "AADSTS70008")
        elif grant == "refresh_token":
            if form.get("refresh_token") not in self.refresh_tokens:
                return _error(400, "invalid_grant", "AADSTS700082")
        else:
            return _error(400, "unsupported_grant_type", "AADSTS70003")
        self.issued += 1
        body = {
            "token_type": "Bearer",
            "scope": self.granted_scope,
            "expires_in": 3599,
            "ext_expires_in": 3599,
            "access_token": f"access-{self.issued}",
            "id_token": self.id_token
            or self.account.id_token(set(form.get("scope", "").split())),
        }
        if self.issue_refresh_token:
            body["refresh_token"] = f"refresh-{self.issued + 1}"
            self.refresh_tokens.add(body["refresh_token"])
        return httpx.Response(200, json=body)


def _error(status: int, error: str, code: str) -> httpx.Response:
    return httpx.Response(
        status,
        json={
            "error": error,
            "error_description": f"{code}: refused by the fake. Trace ID: x",
            "error_codes": [int(code.removeprefix("AADSTS"))],
        },
    )
