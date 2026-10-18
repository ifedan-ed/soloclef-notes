"""Google's OAuth web flow for Drive: the consent URL, and trading its code for tokens.

PKCE is used on top of the client secret. The verifier and the state are the
connect attempt's (``connect_attempt.py``): they live in an HttpOnly cookie
on the browser that started the connect, never in a URL, so any backend
replica can finish the flow but only for that browser.
"""

from __future__ import annotations

import asyncio
import json
from typing import Any

from google.oauth2.credentials import Credentials
from google_auth_oauthlib.flow import Flow

from app.config import config

from .google_endpoints import auth_uri, token_uri
from .scopes import SCOPES


def _flow() -> Flow:
    return Flow.from_client_config(
        {
            "web": {
                "client_id": config.GOOGLE_OAUTH_CLIENT_ID,
                "client_secret": config.GOOGLE_OAUTH_CLIENT_SECRET,
                "auth_uri": auth_uri(),
                "token_uri": token_uri(),
                "redirect_uris": [config.GOOGLE_DRIVE_REDIRECT_URI],
            }
        },
        scopes=SCOPES,
        redirect_uri=config.GOOGLE_DRIVE_REDIRECT_URI,
    )


def consent_url(state: str, code_verifier: str) -> str:
    """Google's consent page for one connect attempt (its S256 challenge only)."""
    flow = _flow()
    flow.code_verifier = code_verifier
    url, _ = flow.authorization_url(
        access_type="offline",
        # Always ask, so Google always returns a refresh token.
        prompt="consent",
        state=state,
    )
    return url


def _exchange(code: str, code_verifier: str | None) -> Credentials:
    flow = _flow()
    flow.code_verifier = code_verifier
    flow.fetch_token(code=code)
    return flow.credentials


async def exchange_code(code: str, code_verifier: str | None) -> Credentials:
    return await asyncio.to_thread(_exchange, code, code_verifier)


def granted_scopes(credentials: Credentials) -> set[str]:
    """What the person actually granted; Google lets them untick scopes."""
    return set(credentials.granted_scopes or credentials.scopes or [])


def credentials_config(credentials: Credentials) -> dict[str, Any]:
    """The credential fields a connector row stores (encrypted by the caller)."""
    return json.loads(credentials.to_json())
