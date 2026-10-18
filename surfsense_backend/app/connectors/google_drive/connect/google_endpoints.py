"""Where Google's OAuth and Drive APIs are. Unset means Google itself.

The overrides exist for the test stand-in (``tests/e2e/fakes/google_server.py``);
production leaves them unset.
"""

from __future__ import annotations

import os

AUTH_URI = "https://accounts.google.com/o/oauth2/auth"
TOKEN_URI = "https://oauth2.googleapis.com/token"
REVOKE_URI = "https://oauth2.googleapis.com/revoke"


def _override(name: str) -> str | None:
    return os.getenv(name, "").strip() or None


def auth_uri() -> str:
    return _override("GOOGLE_OAUTH_AUTH_URI") or AUTH_URI


def token_uri() -> str:
    return _override("GOOGLE_OAUTH_TOKEN_URI") or TOKEN_URI


def revoke_uri() -> str:
    return _override("GOOGLE_OAUTH_REVOKE_URI") or REVOKE_URI


def drive_api_endpoint() -> str | None:
    """The Drive v3 base URL (``…/drive/v3/``) for ``googleapiclient``; ``None``: Google."""
    return _override("GOOGLE_DRIVE_API_ENDPOINT")
