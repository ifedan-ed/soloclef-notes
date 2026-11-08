"""How long a public link lives. Seven days unless the Owner picks otherwise."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from typing import Literal

from pydantic import BaseModel

ExpiryDays = Literal[7, 30]
DEFAULT_EXPIRY_DAYS: ExpiryDays = 7


class PublicLinkCreate(BaseModel):
    """``expires_in_days`` null means the link never expires."""

    expires_in_days: ExpiryDays | None = DEFAULT_EXPIRY_DAYS


def expires_at_for(days: int | None, now: datetime | None = None) -> datetime | None:
    if days is None:
        return None
    return (now or datetime.now(UTC)) + timedelta(days=days)


def is_expired(expires_at: datetime | None, now: datetime | None = None) -> bool:
    return expires_at is not None and expires_at <= (now or datetime.now(UTC))
