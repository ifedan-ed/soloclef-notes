"""Send the reset email in the background, at most a few per address per hour.

``POST /auth/forgot-password`` answers 202 whether or not the address has an
account. Sending inline would make an existing address answer a second or two
slower than an unknown one, so the send runs after the response. A per-address
hourly cap (on top of the per-IP route limit) keeps the form from being used
to flood someone's inbox; a capped request is dropped silently, since saying
so would reveal that the address has an account.

The token is never logged.
"""

from __future__ import annotations

import asyncio
import hashlib
import logging

from app.config import config
from app.mailer import MailerError, get_mailer, is_mail_enabled

from .message import build_reset_email

logger = logging.getLogger(__name__)

_WINDOW_SECONDS = 3600
# Strong references: a bare create_task may be collected before it runs.
_pending: set[asyncio.Task] = set()


def _address_key(email: str) -> str:
    digest = hashlib.sha256(email.strip().lower().encode("utf-8")).hexdigest()
    return f"surfsense:password_reset_mail:{digest}"


def _within_address_cap(email: str) -> bool:
    """Count this send against the address; False once the hour's cap is used."""
    limit = config.PASSWORD_RESET_EMAILS_PER_HOUR
    if limit <= 0:
        return True
    try:
        import redis

        client = redis.from_url(config.REDIS_APP_URL, decode_responses=True)
        pipe = client.pipeline()
        key = _address_key(email)
        pipe.incr(key)
        pipe.expire(key, _WINDOW_SECONDS, nx=True)
        count = pipe.execute()[0]
    except Exception:
        # The per-IP route limit still applies; refusing every reset because
        # Redis blinked would lock people out instead.
        logger.warning("Password reset address cap unavailable; sending anyway")
        return True
    return int(count) <= limit


async def send_reset_email(user_id: object, email: str, token: str) -> bool:
    """Send one reset email now. True when the mail server accepted it."""
    if not is_mail_enabled():
        logger.warning(
            "Password reset requested for user %s but SMTP is off; nothing sent",
            user_id,
        )
        return False
    if not await asyncio.to_thread(_within_address_cap, email):
        logger.info("Password reset email for user %s skipped: hourly cap", user_id)
        return False
    try:
        await get_mailer().send(build_reset_email(to=email, token=token))
    except MailerError as exc:
        logger.error(
            "Password reset email for user %s failed: %s", user_id, type(exc).__name__
        )
        return False
    logger.info("Password reset email sent for user %s", user_id)
    return True


def schedule_reset_email(user_id: object, email: str, token: str) -> None:
    """Send the reset email after the response has gone out."""
    task = asyncio.get_running_loop().create_task(
        send_reset_email(user_id, email, token)
    )
    _pending.add(task)
    task.add_done_callback(_pending.discard)
