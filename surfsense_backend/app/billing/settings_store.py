"""Billing settings: admin-edited in the database, with environment fallbacks.

Plain settings are JSON values. Secrets (Stripe and BTCPay keys, webhook
secrets) are stored Fernet-encrypted under ``SECRET_KEY`` and can be written
and cleared but never read back through any API; ``describe()`` only reports
whether each one is set and where it comes from.

Switching Stripe from test to live is therefore a settings change alone: put
the live secret key and webhook secret in (or in the environment) and nothing
else moves, because every Stripe object id the code keeps is re-created on
demand in whichever mode the key belongs to.
"""

from __future__ import annotations

import os
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config

from .defaults import SETTINGS as DEFAULT_SETTINGS
from .persistence.models import BillingSetting

# secret key -> environment variable it falls back to
SECRET_ENV: dict[str, str] = {
    "stripe_secret_key": "STRIPE_SECRET_KEY",
    "stripe_webhook_secret": "STRIPE_WEBHOOK_SECRET",
    "btcpay_api_key": "BTCPAY_API_KEY",
    "btcpay_webhook_secret": "BTCPAY_WEBHOOK_SECRET",
}
# plain key -> environment variable that overrides the seeded default until an
# admin saves a value
PLAIN_ENV: dict[str, str] = {
    "mode": "BILLING_MODE",
    "stripe_publishable_key": "STRIPE_PUBLISHABLE_KEY",
    "btcpay_url": "BTCPAY_URL",
    "btcpay_store_id": "BTCPAY_STORE_ID",
    # The Report Issue / contact address (app.support_contact).
    "support_email": "SUPPORT_EMAIL",
}
MODES = ("off", "shadow", "live")


def _cipher():
    from app.utils.oauth_security import TokenEncryption

    return TokenEncryption(config.SECRET_KEY)


async def _rows(session: AsyncSession) -> dict[str, BillingSetting]:
    rows = (await session.execute(select(BillingSetting))).scalars().all()
    return {row.key: row for row in rows}


def _plain_value(key: str, row: BillingSetting | None) -> Any:
    if row is not None and row.value is not None:
        return row.value
    env_name = PLAIN_ENV.get(key)
    if env_name and os.getenv(env_name):
        return os.getenv(env_name)
    return DEFAULT_SETTINGS.get(key)


def _secret_value(key: str, row: BillingSetting | None) -> str | None:
    if row is not None and isinstance(row.value, dict) and row.value.get("enc"):
        try:
            return _cipher().decrypt_token(row.value["enc"])
        except ValueError:
            return None
    return os.getenv(SECRET_ENV[key]) or None


class BillingSettings:
    """A snapshot of every setting, read once per use."""

    def __init__(self, rows: dict[str, BillingSetting]):
        self._rows = rows

    def get(self, key: str) -> Any:
        if key in SECRET_ENV:
            return _secret_value(key, self._rows.get(key))
        return _plain_value(key, self._rows.get(key))

    @property
    def mode(self) -> str:
        value = str(self.get("mode") or "off").lower()
        return value if value in MODES else "off"

    def stripe_mode(self) -> str | None:
        """``test`` or ``live`` from the configured key, ``None`` if unset."""
        key = self.get("stripe_secret_key")
        if not key:
            return None
        return "live" if key.startswith(("sk_live_", "rk_live_")) else "test"

    def describe(self) -> dict[str, Any]:
        """Every setting for the admin page; secrets only as set/unset."""
        plain = {key: self.get(key) for key in DEFAULT_SETTINGS}
        for key in PLAIN_ENV:
            plain.setdefault(key, self.get(key))
        secrets = {}
        for key, env_name in SECRET_ENV.items():
            row = self._rows.get(key)
            stored = bool(
                row is not None and isinstance(row.value, dict) and row.value.get("enc")
            )
            secrets[key] = {
                "set": stored or bool(os.getenv(env_name)),
                "source": "admin"
                if stored
                else ("env" if os.getenv(env_name) else None),
            }
        from .payment_methods import statuses

        return {
            "values": plain,
            "secrets": secrets,
            "stripe_mode": self.stripe_mode(),
            # Per method: ready (offered to people), enabled, and what is missing.
            "payments": statuses(self),
        }


async def load_settings(session: AsyncSession) -> BillingSettings:
    return BillingSettings(await _rows(session))


async def save_settings(
    session: AsyncSession,
    *,
    values: dict[str, Any] | None = None,
    secrets: dict[str, str | None] | None = None,
    actor: UUID | None = None,
) -> None:
    """Upsert plain values and write-only secrets (``None``/"" clears one)."""
    rows = await _rows(session)
    now = datetime.now(UTC)
    for key, value in (values or {}).items():
        if key in SECRET_ENV:
            continue
        if key not in DEFAULT_SETTINGS and key not in PLAIN_ENV:
            raise ValueError(f"Unknown billing setting: {key}")
        if key == "mode" and value not in MODES:
            raise ValueError(f"Billing mode must be one of {', '.join(MODES)}")
        if key == "support_email":
            from app.support_contact import clean_support_email

            value = clean_support_email(value)
            if value is None:
                raise ValueError("The support email must be one plain email address")
        row = rows.get(key) or BillingSetting(key=key, secret=False)
        row.value = value
        row.updated_at = now
        row.updated_by = actor
        session.add(row)
    for key, value in (secrets or {}).items():
        if key not in SECRET_ENV:
            raise ValueError(f"Unknown billing secret: {key}")
        row = rows.get(key) or BillingSetting(key=key, secret=True)
        row.value = {"enc": _cipher().encrypt_token(value)} if value else None
        row.secret = True
        row.updated_at = now
        row.updated_by = actor
        session.add(row)
    await session.flush()
