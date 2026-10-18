"""Authentication schemas for refresh token endpoints."""

from datetime import datetime

from pydantic import BaseModel, Field


class RefreshTokenRequest(BaseModel):
    """Request body for token refresh endpoint."""

    refresh_token: str | None = None


class RefreshTokenResponse(BaseModel):
    """Response from token refresh endpoint."""

    access_token: str
    refresh_token: str | None = None
    token_type: str = "bearer"
    access_expires_at: int
    #: The refresh token's family: one per sign-in (a device). The phone app
    #: keeps it to tell its own row in the signed-in devices list.
    session_id: str | None = None


class AppSessionResponse(RefreshTokenResponse):
    """What the phone app's sign-in exchange answers with."""

    #: Example SSO's own sign-out page (RP-Initiated Logout), when it has one;
    #: the app's "Also sign out of Example SSO" (Android) opens it.
    end_session_url: str | None = None


class AppExchangeRequest(BaseModel):
    """The phone app spending its one-time code (app_sign_in.py)."""

    code: str = Field(min_length=1, max_length=256)
    verifier: str = Field(min_length=43, max_length=128)
    device_label: str = Field(default="", max_length=100)


class DeviceRead(BaseModel):
    """One signed-in device (a refresh-token family) of the account."""

    session_id: str
    kind: str
    device_label: str | None = None
    signed_in_at: datetime
    last_used_at: datetime
    expires_at: datetime


class LogoutRequest(BaseModel):
    """Request body for logout endpoint (current device)."""

    refresh_token: str | None = None


class LogoutResponse(BaseModel):
    """Response from logout endpoint (current device)."""

    detail: str = "Successfully logged out"


class LogoutAllResponse(BaseModel):
    """Response from logout all devices endpoint."""

    detail: str = "Successfully logged out from all devices"


class SessionResponse(BaseModel):
    authenticated: bool = True
    access_expires_at: int | None = None


class DesktopSessionRequest(BaseModel):
    code: str
    code_verifier: str
    redirect_uri: str


class DesktopLoginRequest(BaseModel):
    email: str
    password: str
    #: Sent by the phone app ("iPhone 15 Pro · Soloclef app"): its session is
    #: then an app session (365 days, device-bound) and the device is listed
    #: under signed-in devices. Absent (the desktop app), the usual web lifetimes.
    device_label: str | None = Field(default=None, max_length=100)
