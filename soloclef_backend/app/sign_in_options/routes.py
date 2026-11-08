"""``GET /sign-in-options`` (under /api/v1): the ways this server takes, for anyone.

Public and read-only, like ``/support-contact``: the phone app asks it before
anyone has signed in (the module's docstring says what each field means).
The settings are read once, when the server starts, as ``app.py`` reads them
to mount the sign-in routes; an answer may be kept for 60 s on the way.
"""

from __future__ import annotations

import os
from functools import lru_cache
from typing import Literal

from fastapi import APIRouter, Response
from pydantic import BaseModel

from app.auth_oidc.settings import load_oidc_settings
from app.config import config

from . import sign_in_options

router = APIRouter(tags=["auth"])


class SsoSignIn(BaseModel):
    label: str
    name: str
    app: bool


class SignInOptions(BaseModel):
    service: Literal["soloclef"]
    password: bool
    forgot_password: bool
    sign_up: bool
    sso: SsoSignIn | None


@lru_cache(maxsize=1)
def _options() -> dict:
    oidc = load_oidc_settings(
        backend_url=config.BACKEND_URL, frontend_url=config.NEXT_FRONTEND_URL
    )
    return sign_in_options(
        auth_type=config.AUTH_TYPE,
        registration_enabled=config.REGISTRATION_ENABLED,
        oidc=oidc,
        env=os.environ,
    )


@router.get("/sign-in-options", response_model=SignInOptions)
async def get_sign_in_options(response: Response) -> dict:
    response.headers["Cache-Control"] = "public, max-age=60"
    return _options()
