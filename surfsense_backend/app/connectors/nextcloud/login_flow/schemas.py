"""What the connect API returns. No token or password ever appears here."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel


class NextcloudAccountRead(BaseModel):
    user_id: str
    display_name: str


class NextcloudConnectionRead(BaseModel):
    #: False when the admin has not set NEXTCLOUD_BASE_URL.
    configured: bool
    #: The server people connect to, as a label (``cloud.example.com``).
    server: str | None
    connector_id: int | None = None
    account: NextcloudAccountRead | None = None
    #: The stored login is unusable (other server, incomplete): connect again.
    needs_reconnect: bool = False


class LoginStarted(BaseModel):
    flow_id: str
    #: Opened by the person in a new tab to sign in and grant access.
    login_url: str
    expires_in: int


class LoginProgress(BaseModel):
    status: Literal["pending", "connected"]
    connection: NextcloudConnectionRead | None = None


class Disconnected(BaseModel):
    #: Whether Nextcloud confirmed the app password is gone.
    revoked: bool
