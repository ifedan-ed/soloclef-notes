"""Which Google account a fresh grant belongs to, from Drive's ``about`` resource."""

from __future__ import annotations

import asyncio
import logging

from google.oauth2.credentials import Credentials

from .drive_service import drive_service

logger = logging.getLogger(__name__)


def _about(credentials: Credentials) -> dict:
    return (
        drive_service(credentials)
        .about()
        .get(fields="user(emailAddress,displayName)")
        .execute()
    )


async def account_email(credentials: Credentials) -> str | None:
    try:
        about = await asyncio.to_thread(_about, credentials)
    except Exception as exc:
        logger.warning("Reading the Google account failed: %s", type(exc).__name__)
        return None
    email = (about.get("user") or {}).get("emailAddress")
    return str(email) if email else None
