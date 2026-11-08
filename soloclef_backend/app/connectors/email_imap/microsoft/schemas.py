"""What the Microsoft connect routes take and return. Never a token."""

from __future__ import annotations

from pydantic import BaseModel, Field


class MicrosoftMailAvailability(BaseModel):
    #: False until the administrator set MICROSOFT_MAIL_CLIENT_ID and _SECRET.
    configured: bool


class MicrosoftConnectRequest(BaseModel):
    #: The app page to come back to (a path on this site); else the connectors page.
    return_path: str | None = Field(default=None, max_length=512)
    #: Reconnecting this mailbox: Microsoft's sign-in preselects its address.
    account_id: str | None = Field(default=None, max_length=64)


class MicrosoftConnectStarted(BaseModel):
    #: Microsoft's sign-in and consent page; the browser goes there.
    auth_url: str
