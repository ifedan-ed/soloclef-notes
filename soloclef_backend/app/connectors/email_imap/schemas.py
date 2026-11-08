from __future__ import annotations

from datetime import date
from typing import Any

from pydantic import BaseModel, Field


class EmailAccountRead(BaseModel):
    id: str
    label: str
    host: str
    port: int
    use_tls: bool
    username: str
    created_at: str | None = None
    #: "password" (added with the form) or "microsoft" (Microsoft's sign-in).
    auth: str = "password"
    #: Microsoft stopped accepting this mailbox's grant: connect it again.
    needs_reconnect: bool = False


class EmailAccountCreate(BaseModel):
    label: str = Field("", max_length=200)
    host: str = Field(..., min_length=1, max_length=255)
    port: int = Field(993, ge=1, le=65535)
    use_tls: bool = True
    username: str = Field(..., min_length=1, max_length=320)
    app_password: str = Field(..., min_length=1, max_length=2000)


class RawTestConnectionRequest(BaseModel):
    """For testing a connection before the account is saved (the add-account
    form's "Test connection" button) — never persisted."""

    host: str = Field(..., min_length=1, max_length=255)
    port: int = Field(993, ge=1, le=65535)
    use_tls: bool = True
    username: str = Field(..., min_length=1, max_length=320)
    app_password: str = Field(..., min_length=1, max_length=2000)


class TestConnectionResult(BaseModel):
    status: str
    folder_count: int | None = None
    message: str | None = None


class SearchEmailsRequest(BaseModel):
    account_id: str | None = None
    folder: str = "INBOX"
    text: str | None = None
    from_: str | None = Field(None, alias="from")
    to: str | None = None
    subject: str | None = None
    since: date | None = None
    before: date | None = None
    limit: int = Field(20, ge=1, le=50)

    model_config = {"populate_by_name": True}


class EmailSummary(BaseModel):
    uid: str | None = None
    account_id: str | None = None
    account_label: str | None = None
    folder: str | None = None
    from_: str | None = Field(None, alias="from")
    to: str | None = None
    subject: str | None = None
    date: str | None = None
    error: str | None = None

    model_config = {"populate_by_name": True}


class SearchEmailsResponse(BaseModel):
    results: list[dict[str, Any]]
