"""What the Drive connect API returns. No token except the Picker's short-lived one."""

from __future__ import annotations

from pydantic import BaseModel


class GoogleDriveConnectionRead(BaseModel):
    #: False until the admin has set every setting in ``missing``.
    configured: bool
    #: Names of unset server settings (never values).
    missing: list[str] = []
    connector_id: int | None = None
    account_email: str | None = None
    #: Google refused the stored grant (revoked or expired): connect again.
    needs_reconnect: bool = False


class ConnectStarted(BaseModel):
    #: Google's consent page; the browser goes there and Google sends it back.
    auth_url: str


class PickerSession(BaseModel):
    """What the Google Picker needs in the browser, for this person's own grant."""

    access_token: str
    api_key: str
    app_id: str


class Disconnected(BaseModel):
    #: Whether Google confirmed the grant is gone.
    revoked: bool
