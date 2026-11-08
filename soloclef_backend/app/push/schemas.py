"""Request and response shapes of the push API."""

from __future__ import annotations

from datetime import datetime
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, Field, field_validator

from app.push.kinds import KIND_NAMES

# Expo's token shape: "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]" (or ExpoPushToken[...]).
TOKEN_MAX = 255
LABEL_MAX = 120
LANGUAGE_MAX = 16


def is_expo_token(value: str) -> bool:
    return (
        value.startswith(("ExponentPushToken[", "ExpoPushToken["))
        and value.endswith("]")
        and len(value) <= TOKEN_MAX
        and not any(ch.isspace() for ch in value)
    )


class DeviceRegistration(BaseModel):
    token: str
    platform: Literal["ios", "android"]
    label: str | None = None
    language: str | None = None
    # The phone's sign-in (the ``session_id`` its sign-in or refresh answered,
    # a refresh-token family), required: it must be this account's live
    # sign-in (else 401), and the phone then takes pushes only while that
    # sign-in lasts. Every app build sends it (nothing installed predates c0026).
    session_id: UUID

    @field_validator("token")
    @classmethod
    def _token(cls, value: str) -> str:
        value = value.strip()
        if not is_expo_token(value):
            raise ValueError("Not an Expo push token.")
        return value

    @field_validator("label")
    @classmethod
    def _label(cls, value: str | None) -> str | None:
        value = " ".join((value or "").split())
        return value[:LABEL_MAX] or None

    @field_validator("language")
    @classmethod
    def _language(cls, value: str | None) -> str | None:
        value = (value or "").strip()
        return value[:LANGUAGE_MAX] or None


class DeviceRemoval(BaseModel):
    """``DELETE /push/devices`` with the token in the body, out of access logs."""

    token: str = Field(min_length=1, max_length=TOKEN_MAX + 16)


class DeviceOut(BaseModel):
    id: int
    token: str
    platform: str
    label: str | None
    language: str | None
    last_seen_at: datetime


class KindOut(BaseModel):
    kind: str
    label: str
    description: str


class PreferencesOut(BaseModel):
    """Every kind with its choice, so the app draws one switch per kind."""

    choices: dict[str, bool]
    kinds: list[KindOut]


class PreferencesIn(BaseModel):
    choices: dict[str, bool]

    @field_validator("choices")
    @classmethod
    def _choices(cls, value: dict[str, bool]) -> dict[str, bool]:
        unknown = sorted(k for k in value if k not in KIND_NAMES)
        if unknown:
            raise ValueError(f"Unknown notification kinds: {', '.join(unknown)}.")
        return value
