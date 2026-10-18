"""Where people send problem reports: the "Report Issue" address.

An admin edits it on User settings -> Billing -> Admin -> Settings (the
``support_email`` key in ``billing_settings``, the app's admin-edited
key/value store). Until one is saved it is ``SUPPORT_EMAIL`` from
``docker/.env`` (``app.billing.settings_store.PLAIN_ENV``); a value that does
not look like one plain address counts as unset.

With neither there is no address: a new server must not send its users'
reports to some other server's operator. The endpoint then answers
``{"email": ""}``, the website's Report Issue mail opens with no recipient,
and the contact page says no address is published.
"""

from __future__ import annotations

import re
from typing import TYPE_CHECKING

from sqlalchemy.ext.asyncio import AsyncSession

if TYPE_CHECKING:
    from app.billing.settings_store import BillingSettings

DEFAULT_SUPPORT_EMAIL = ""
SUPPORT_EMAIL_KEY = "support_email"
MAX_EMAIL_CHARS = 254

_EMAIL = re.compile(r"^[^@\s<>\"',;:]+@[^@\s<>\"',;:]+\.[^@\s<>\"',;:]+$")


def clean_support_email(value: object) -> str | None:
    """The address trimmed, or None when it is not a plain email address."""
    if not isinstance(value, str):
        return None
    text = value.strip()
    if not text or len(text) > MAX_EMAIL_CHARS or not _EMAIL.match(text):
        return None
    return text


def published_support_email(settings: BillingSettings) -> str:
    """The admin's address, else SUPPORT_EMAIL, else ``""`` (none published)."""
    return clean_support_email(settings.get(SUPPORT_EMAIL_KEY)) or (
        DEFAULT_SUPPORT_EMAIL
    )


async def support_email(session: AsyncSession) -> str:
    from app.billing.settings_store import load_settings

    return published_support_email(await load_settings(session))
