"""Where an invite stands, as the Team page and the invite page show it."""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Literal

InviteStatus = Literal["pending", "used_up", "expired", "revoked"]


def invite_status(invite, now: datetime | None = None) -> InviteStatus:
    now = now or datetime.now(UTC)
    if invite.max_uses and invite.uses_count >= invite.max_uses:
        return "used_up"
    if not invite.is_active:
        return "revoked"
    if invite.expires_at and invite.expires_at < now:
        return "expired"
    return "pending"
