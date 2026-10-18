"""What "Connect your agent" shows for claude.ai and ChatGPT.

Both need the Authentik client id (and its secret) pasted into their custom
connector form, since Authentik offers them no registration they can use (see
custom/README.md). Signed-in people only: the secret is shown to every account
because every account may connect its own Claude or ChatGPT, and it lets
nobody sign in by itself. Authentik only ever returns a sign-in to claude.ai's
and chatgpt.com's callbacks, after the person's own login, and only to people
it lets into Soloclef Notes.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends

from app.agent_sign_in.principal import agent_settings
from app.auth.context import AuthContext
from app.users import require_session_context

router = APIRouter(tags=["agent-sign-in"])


@router.get("/agent-sign-in")
async def agent_sign_in_details(
    _auth: AuthContext = Depends(require_session_context),
):
    settings = agent_settings()
    if not settings.enabled:
        return {"enabled": False, "client_id": None, "client_secret": None}
    return {
        "enabled": True,
        "client_id": settings.client_id,
        "client_secret": settings.client_secret or None,
    }
