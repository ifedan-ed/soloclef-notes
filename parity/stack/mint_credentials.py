#!/usr/bin/env python3
"""Mint the credentials the comparison sends, one per name a case can ask for.

They are minted rather than committed for two reasons: a committed JWT expires
and the suite rots, and a token in the repository is a token in the public
mirror. Everything here is signed with the made-up session secret in
stack/world.sh and names accounts that exist only in stack/seed.sql.

A case names a credential (`"as": "owner"`); the headers that credential
contributes are what this writes. `none` contributes nothing, which is how a
case asks for no credential at all.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import sys
import time

# stack/seed.sql's accounts.
ACCOUNTS = {
    "owner": "11111111-1111-1111-1111-111111111111",
    "scribe": "22222222-2222-2222-2222-222222222222",
    "bare": "33333333-3333-3333-3333-333333333333",
    "outsider": "44444444-4444-4444-4444-444444444444",
    "inactive": "55555555-5555-5555-5555-555555555555",
    "remover": "66666666-6666-6666-6666-666666666666",
    "sole": "77777777-7777-7777-7777-777777777777",
}

# fastapi-users' own audience, which both tiers check.
AUDIENCE = ["fastapi-users:auth"]


def segment(payload: dict) -> bytes:
    raw = json.dumps(payload, separators=(",", ":")).encode()
    return base64.urlsafe_b64encode(raw).rstrip(b"=")


def sign(claims: dict, secret: str, header: dict | None = None) -> str:
    head = segment(header or {"alg": "HS256", "typ": "JWT"})
    body = segment(claims)
    signed = head + b"." + body
    mac = hmac.new(secret.encode(), signed, hashlib.sha256).digest()
    return (signed + b"." + base64.urlsafe_b64encode(mac).rstrip(b"=")).decode()


def session_token(user_id: str, secret: str, lifetime: int) -> str:
    issued = int(time.time())
    return sign(
        {"sub": user_id, "aud": AUDIENCE, "exp": issued + lifetime, "iat": issued},
        secret,
    )


def credentials(secret: str, cookie_name: str) -> dict[str, dict[str, str]]:
    hour = 3600
    minted: dict[str, dict[str, str]] = {"none": {}}

    for name, user_id in ACCOUNTS.items():
        minted[name] = {"Authorization": "Bearer " + session_token(user_id, secret, hour)}

    # The session cookie rather than the header, which is the credential the web
    # app sends and the only one the CSRF origin check applies to.
    minted["cookie_owner"] = {
        "Cookie": f"{cookie_name}={session_token(ACCOUNTS['owner'], secret, hour)}"
    }
    minted["cookie_scribe"] = {
        "Cookie": f"{cookie_name}={session_token(ACCOUNTS['scribe'], secret, hour)}"
    }

    # The API keys, which are the literal strings stack/seed.sql stores the
    # sha256 of.
    minted["key_owner"] = {"Authorization": "Bearer ss_pat_parity_owner_key"}
    minted["key_scribe"] = {"Authorization": "Bearer ss_pat_parity_scribe_key"}
    minted["key_unknown"] = {"Authorization": "Bearer ss_pat_parity_no_such_key"}

    # The malformed and the stale, each a different refusal.
    minted["malformed"] = {"Authorization": "Bearer not-a-token-at-all"}
    minted["wrong_signature"] = {
        "Authorization": "Bearer " + session_token(ACCOUNTS["owner"], secret + "-wrong", hour)
    }
    minted["expired"] = {
        "Authorization": "Bearer " + session_token(ACCOUNTS["owner"], secret, -hour)
    }
    minted["wrong_audience"] = {
        "Authorization": "Bearer "
        + sign(
            {
                "sub": ACCOUNTS["owner"],
                "aud": ["fastapi-users:reset"],
                "exp": int(time.time()) + hour,
                "iat": int(time.time()),
            },
            secret,
        )
    }
    minted["bearer_nothing"] = {"Authorization": "Bearer "}
    minted["wrong_scheme"] = {
        "Authorization": "Basic " + base64.b64encode(b"owner:x").decode()
    }

    # An agent's token: a provider this deployment does not know, so neither
    # tier can verify it and each has to decide what to do with it. Signed with
    # a secret of its own, because its signature is never the reason for the
    # answer.
    minted["agent_foreign_issuer"] = {
        "Authorization": "Bearer "
        + sign(
            {
                "iss": "https://other.example.invalid/application/o/parity/",
                "sub": "s",
                "aud": "a",
                "exp": int(time.time()) + hour,
            },
            "a-provider-this-deployment-does-not-know",
            {"alg": "HS256", "typ": "JWT", "kid": "x"},
        )
    }
    return minted


def main() -> int:
    secret = os.environ["PARITY_SESSION_SECRET"]
    cookie = os.environ.get("PARITY_SESSION_COOKIE_NAME", "surfsense_session")
    destination = sys.argv[1]
    with open(destination, "w", encoding="utf-8") as out:
        json.dump(credentials(secret, cookie), out, indent=1, sort_keys=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
