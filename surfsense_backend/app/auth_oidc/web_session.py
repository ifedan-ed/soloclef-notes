"""Hand a signed-in person their Notes session, as a password sign-in does."""

from __future__ import annotations

from fastapi import Request
from fastapi.responses import RedirectResponse

from app.auth.session_cookies import write_session
from app.db import User
from app.users import get_jwt_strategy
from app.utils.refresh_tokens import create_refresh_token


async def signed_in_redirect(
    user: User, request: Request, frontend_url: str, path: str
) -> RedirectResponse:
    """Session cookies (the usual access and refresh lifetimes), then ``path``."""
    access = await get_jwt_strategy().write_token(user)
    refresh = await create_refresh_token(user.id)
    response = RedirectResponse(f"{frontend_url}{path}", status_code=303)
    write_session(response, access, refresh, request)
    return response
