"""Microsoft's sign-in and consent page for one connect attempt."""

from __future__ import annotations

from urllib.parse import urlencode

from .connect_attempt import ConnectAttempt
from .settings import SCOPES, MicrosoftMailSettings


def consent_url(
    settings: MicrosoftMailSettings,
    attempt: ConnectAttempt,
    *,
    login_hint: str | None = None,
) -> str:
    """Authorization-code request with PKCE (S256).

    ``prompt=select_account`` always shows Microsoft's account picker, so a
    mailbox is never linked without the person choosing it there, and someone
    signed in to several Microsoft accounts picks the right one.
    ``login_hint`` preselects the mailbox being reconnected.
    """
    params = {
        "client_id": settings.client_id,
        "response_type": "code",
        "redirect_uri": settings.redirect_uri,
        "response_mode": "query",
        "scope": " ".join(SCOPES),
        "state": attempt.state,
        "code_challenge": attempt.code_challenge,
        "code_challenge_method": "S256",
        "prompt": "select_account",
    }
    if login_hint:
        params["login_hint"] = login_hint
    return f"{settings.authorize_url}?{urlencode(params)}"
