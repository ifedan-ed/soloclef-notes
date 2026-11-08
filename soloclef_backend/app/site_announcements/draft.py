"""What an administrator may send: the rules a draft announcement must meet."""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Literal

from pydantic import BaseModel, field_validator, model_validator

TITLE_MAX = 120
BODY_MAX = 2000
LINK_MAX = 2048

Audience = Literal["all", "workspace"]


class AnnouncementDraft(BaseModel):
    """A title, a short Markdown-lite body, an optional link and expiry, and who gets it."""

    title: str
    body: str
    link: str | None = None
    expires_at: datetime | None = None
    audience: Audience = "all"
    workspace_id: int | None = None

    @field_validator("title")
    @classmethod
    def _title(cls, value: str) -> str:
        value = " ".join(value.split())
        if not value:
            raise ValueError("Give the announcement a title.")
        if len(value) > TITLE_MAX:
            raise ValueError(f"Keep the title under {TITLE_MAX} characters.")
        return value

    @field_validator("body")
    @classmethod
    def _body(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError("Write the announcement.")
        if len(value) > BODY_MAX:
            raise ValueError(f"Keep the announcement under {BODY_MAX} characters.")
        return value

    @field_validator("link")
    @classmethod
    def _link(cls, value: str | None) -> str | None:
        value = (value or "").strip()
        if not value:
            return None
        if len(value) > LINK_MAX:
            raise ValueError("The link is too long.")
        # A page in the app ("/dashboard/...") or a web address; nothing that runs.
        # "//host" and "/\\host" are protocol-relative to a browser: off-site.
        in_app = value.startswith("/") and value[1:2] not in ("/", "\\")
        on_web = value.lower().startswith(("https://", "http://"))
        if not (in_app or on_web) or any(ch.isspace() for ch in value):
            raise ValueError("The link must be a web address or a page in the app.")
        return value

    @field_validator("expires_at")
    @classmethod
    def _expires_at(cls, value: datetime | None) -> datetime | None:
        if value is None:
            return None
        if value.tzinfo is None:
            value = value.replace(tzinfo=UTC)
        if value <= datetime.now(UTC):
            raise ValueError("The expiry must be in the future.")
        return value

    @model_validator(mode="after")
    def _audience(self) -> AnnouncementDraft:
        if self.audience == "workspace" and self.workspace_id is None:
            raise ValueError("Choose the workspace to send to.")
        if self.audience == "all":
            self.workspace_id = None
        return self
