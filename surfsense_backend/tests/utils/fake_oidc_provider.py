"""A stand-in OpenID Connect provider (shaped like Authentik), in process.

It answers Notes' outgoing calls through an ``httpx.MockTransport`` and signs
real RS256 id_tokens, so the code under test verifies signatures against a
real JWKS exactly as it does against sso.example.com. ``approve`` plays the
person signing in at the provider: it takes the URL Notes redirected the
browser to and returns the callback URL the provider would send them back to.
"""

from __future__ import annotations

import base64
import hashlib
import json
import secrets
import time
from dataclasses import dataclass, field
from urllib.parse import parse_qs, urlencode, urlsplit

import httpx
import jwt
from cryptography.hazmat.primitives.asymmetric import rsa

ISSUER = "https://sso.test/application/o/notes/"
DISCOVERY_URL = f"{ISSUER}.well-known/openid-configuration"
CLIENT_ID = "soloclef-notes"
CLIENT_SECRET = "test-client-secret"
REDIRECT_URI = "https://notes.test/auth/oidc/callback"
# Authentik's access tokens run to ~2,300 characters; Notes must not store one.
LONG_ACCESS_TOKEN = "a" * 2300


def _new_key(kid: str):
    private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    public_jwk = json.loads(jwt.algorithms.RSAAlgorithm.to_jwk(private.public_key()))
    public_jwk.update(kid=kid, use="sig", alg="RS256")
    return private, public_jwk


@dataclass
class _Grant:
    nonce: str
    challenge: str
    redirect_uri: str
    claims: dict


@dataclass
class FakeOidcProvider:
    issuer: str = ISSUER
    client_id: str = CLIENT_ID
    client_secret: str = CLIENT_SECRET
    #: Claims to change or add on the next id_tokens (``None`` removes one).
    id_token_overrides: dict = field(default_factory=dict)
    discovery_requests: int = 0
    jwks_requests: int = 0
    token_requests: list[dict] = field(default_factory=list)
    _grants: dict[str, _Grant] = field(default_factory=dict)

    def __post_init__(self) -> None:
        self.kid = "key-1"
        self._private, self._public_jwk = _new_key(self.kid)

    # -- what the person does at the provider --------------------------------

    def approve(
        self,
        authorize_url: str,
        *,
        sub: str,
        email: str,
        email_verified: bool = True,
        name: str = "",
    ) -> str:
        query = {k: v[0] for k, v in parse_qs(urlsplit(authorize_url).query).items()}
        assert query["client_id"] == self.client_id
        assert query["response_type"] == "code"
        assert query["code_challenge_method"] == "S256"
        code = secrets.token_urlsafe(16)
        self._grants[code] = _Grant(
            nonce=query["nonce"],
            challenge=query["code_challenge"],
            redirect_uri=query["redirect_uri"],
            claims={
                "sub": sub,
                "email": email,
                "email_verified": email_verified,
                "name": name,
            },
        )
        back = urlencode({"code": code, "state": query["state"]})
        return f"{query['redirect_uri']}?{back}"

    def sign(self, claims: dict) -> str:
        """An id_token signed with the provider's current key."""
        return jwt.encode(
            claims, self._private, algorithm="RS256", headers={"kid": self.kid}
        )

    def standard_claims(self, expected_nonce: str, **changes) -> dict:
        now = int(time.time())
        claims = {
            "iss": self.issuer,
            "aud": self.client_id,
            "iat": now,
            "exp": now + 300,
            "nonce": expected_nonce,
            "sub": "subject-1",
            "email": "person@example.com",
            "email_verified": True,
            "name": "A Person",
        }
        claims.update(changes)
        return claims

    def rotate_key(self) -> None:
        """Start signing with a new key id, as after a certificate rotation."""
        self.kid = f"key-{secrets.token_hex(2)}"
        self._private, self._public_jwk = _new_key(self.kid)

    # -- the provider's HTTP side --------------------------------------------

    @property
    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._handle)

    def _discovery(self) -> dict:
        return {
            "issuer": self.issuer,
            "authorization_endpoint": "https://sso.test/application/o/authorize/",
            "token_endpoint": "https://sso.test/application/o/token/",
            "jwks_uri": f"{self.issuer}jwks/",
            "id_token_signing_alg_values_supported": ["RS256"],
            "code_challenge_methods_supported": ["plain", "S256"],
        }

    def _handle(self, request: httpx.Request) -> httpx.Response:
        url = str(request.url)
        if url == DISCOVERY_URL.replace(ISSUER, self.issuer):
            self.discovery_requests += 1
            return httpx.Response(200, json=self._discovery())
        if url == f"{self.issuer}jwks/":
            self.jwks_requests += 1
            return httpx.Response(200, json={"keys": [self._public_jwk]})
        if url == "https://sso.test/application/o/token/":
            return self._token(request)
        return httpx.Response(404, json={"error": "not_found"})

    def _token(self, request: httpx.Request) -> httpx.Response:
        form = {k: v[0] for k, v in parse_qs(request.content.decode()).items()}
        self.token_requests.append(form)
        basic = request.headers.get("authorization", "")
        expected = base64.b64encode(
            f"{self.client_id}:{self.client_secret}".encode()
        ).decode()
        if basic != f"Basic {expected}":
            return httpx.Response(401, json={"error": "invalid_client"})
        grant = self._grants.pop(form.get("code", ""), None)
        if grant is None or form.get("grant_type") != "authorization_code":
            return httpx.Response(400, json={"error": "invalid_grant"})
        if form.get("redirect_uri") != grant.redirect_uri:
            return httpx.Response(400, json={"error": "invalid_grant"})
        verifier = form.get("code_verifier", "")
        digest = hashlib.sha256(verifier.encode()).digest()
        if base64.urlsafe_b64encode(digest).rstrip(b"=").decode() != grant.challenge:
            return httpx.Response(400, json={"error": "invalid_grant"})

        now = int(time.time())
        claims = {
            "iss": self.issuer,
            "aud": self.client_id,
            "iat": now,
            "exp": now + 300,
            "nonce": grant.nonce,
            **grant.claims,
        }
        for key, value in self.id_token_overrides.items():
            if value is None:
                claims.pop(key, None)
            else:
                claims[key] = value
        id_token = self.sign(claims)
        return httpx.Response(
            200,
            json={
                "access_token": LONG_ACCESS_TOKEN,
                "token_type": "Bearer",
                "expires_in": 3600,
                "id_token": id_token,
            },
        )
