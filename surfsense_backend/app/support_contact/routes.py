"""``GET /support-contact``: the Report Issue address, for anyone (no sign-in).

Error pages and toasts can show before or without a session, so this is
public; it returns only the address an admin chose to publish.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends, Response
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import get_async_session

from . import support_email

router = APIRouter()


@router.get("/support-contact")
async def get_support_contact(
    response: Response, session: AsyncSession = Depends(get_async_session)
) -> dict[str, str]:
    response.headers["Cache-Control"] = "public, max-age=60"
    return {"email": await support_email(session)}
