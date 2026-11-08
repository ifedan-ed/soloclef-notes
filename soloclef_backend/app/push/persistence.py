"""The phones that take pushes, each person's choices, and the tickets Expo gave us.

Migrations ``c0025_push_devices`` and ``c0026_push_device_session``. None of these tables is in the Zero
publication: the app reads them over REST.
"""

from __future__ import annotations

from datetime import UTC, datetime

from sqlalchemy import TIMESTAMP, Column, ForeignKey, String, text
from sqlalchemy.dialects.postgresql import JSONB, UUID

from app.db import Base, BaseModel, TimestampMixin


class PushDevice(BaseModel, TimestampMixin):
    """One phone's Expo push token, for one account.

    A token belongs to one account at a time: signing into another account on
    the same phone moves it (``store.upsert_device``), so the first account's
    pushes never reach a phone that no longer shows it.
    """

    __tablename__ = "push_devices"

    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    # A unique index named as c0025 names it (ix_push_devices_token), so a
    # database built by create_all (env.py's fresh install) downgrades too.
    token = Column(String(255), nullable=False, unique=True, index=True)
    # "ios" or "android".
    platform = Column(String(16), nullable=False)
    # "iPhone 15 Pro · Soloclef app": shown nowhere yet; for a devices list later.
    label = Column(String(120), nullable=True)
    # The phone's language, for push text in the web's six languages later.
    language = Column(String(16), nullable=True)
    # The sign-in (refresh-token family, ``RefreshToken.family_id``) the phone
    # registered under (migration c0026). Nullable for rows older than c0026,
    # but every registration now names a live one (``routes.register_device``
    # answers 401 otherwise). The phone takes pushes only while that sign-in is
    # live (``store.devices_of``), and its row goes when the sign-in is revoked
    # (``app/utils/refresh_tokens.py``): signing a lost phone out from the
    # devices list, signing out everywhere or changing the password stops its
    # pushes even though the phone never says so, and it cannot register again.
    session_family_id = Column(UUID(as_uuid=True), nullable=True, index=True)
    last_seen_at = Column(
        TIMESTAMP(timezone=True), nullable=False, default=lambda: datetime.now(UTC)
    )


class PushPreference(Base):
    """A person's per-kind choices (``kinds.py``); a missing row means every kind on."""

    __tablename__ = "push_preferences"

    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        primary_key=True,
    )
    choices = Column(JSONB, nullable=False, default=dict, server_default=text("'{}'"))
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        onupdate=lambda: datetime.now(UTC),
    )


class PushTicket(BaseModel, TimestampMixin):
    """A ticket Expo gave for one sent message, until its receipt has been read."""

    __tablename__ = "push_tickets"

    # A unique index, ix_push_tickets_ticket_id, as c0025 makes it.
    ticket_id = Column(String(64), nullable=False, unique=True, index=True)
    token = Column(String(255), nullable=False)
