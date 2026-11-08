"""Queue the reset email in the background, at most a few per address per hour.

``POST /auth/forgot-password`` answers 202 whether or not the address has an
account. Even queueing touches Redis, so it runs after the response and an
existing address answers no slower than an unknown one; the mail senders
deliver it (``app/mail_queue``) and drop it once the link has expired. A per-address
hourly cap (on top of the per-IP route limit) keeps the form from being used
to flood someone's inbox; a capped request is dropped silently, since saying
so would reveal that the address has an account.

The token is never logged.
"""

from __future__ import annotations

import asyncio
import hashlib
import logging
import time
from dataclasses import replace

from app.config import config
from app.mail_queue import MailQueueUnavailableError, enqueue_email
from app.mailer import is_mail_enabled

from .message import build_reset_email

logger = logging.getLogger(__name__)

_WINDOW_SECONDS = 3600
# Strong references: a bare create_task may be collected before it runs.
_pending: set[asyncio.Task] = set()


def reset_mail_key(token: str) -> str:
    """What identifies this reset email: the token the link in it carries.

    The same token is the same email, so a request answered twice -- a retried
    POST, a double-submitted form -- mails once. A fresh request mints a fresh
    token, and a second reset email is exactly what the person asking for one
    wants, so the token and not the account is what goes in the key. Hashed:
    the key is kept in Redis and the token is not ours to leave lying about.

    fastapi-users puts a freshly salted password hash in every token, so two
    separate requests never share one: this collapses the same token queued
    twice, and nothing else.
    """
    return f"password_reset:{hashlib.sha256(token.encode('utf-8')).hexdigest()}"


def _address_key(email: str) -> str:
    digest = hashlib.sha256(email.strip().lower().encode("utf-8")).hexdigest()
    return f"soloclef:password_reset_mail:{digest}"


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
    """Queue one reset email. True when the queue took it."""
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
        await enqueue_email(
            replace(
                build_reset_email(to=email, token=token),
                idempotency_key=reset_mail_key(token),
            ),
            kind="password_reset",
            ref=user_id,
            not_after=time.time() + config.PASSWORD_RESET_TOKEN_LIFETIME_SECONDS,
        )
    except MailQueueUnavailableError as exc:
        logger.error("Password reset email for user %s not queued: %s", user_id, exc)
        return False
    return True


def schedule_reset_email(user_id: object, email: str, token: str) -> None:
    """Queue the reset email after the response has gone out."""
    task = asyncio.get_running_loop().create_task(
        send_reset_email(user_id, email, token)
    )
    _pending.add(task)
    task.add_done_callback(_pending.discard)
