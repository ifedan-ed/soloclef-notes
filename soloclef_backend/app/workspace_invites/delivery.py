"""Queue the invitation email and say whether the queue took it.

The person inviting sees at once whether the email is on its way, and can
copy the link instead when it is not. The senders drop it unsent if the
invitation has expired by then.
"""

from __future__ import annotations

import logging
from dataclasses import replace
from datetime import datetime

from app.mail_queue import MailQueueUnavailableError, enqueue_email
from app.mailer import is_mail_enabled
from app.mailer.protocol import OutboundEmail

logger = logging.getLogger(__name__)


def invite_mail_key(invite_id: int, sent_at: datetime) -> str:
    """What identifies this invitation email: the occasion it was sent on.

    A resend keeps the invitation's code and bumps ``last_sent_at``, so the
    invitation alone would make every resend a duplicate of the first and
    silently send nothing. The occasion is the invitation and the moment it
    was sent for, which makes one resend one email however many times the
    request is repeated.
    """
    return f"invite:{invite_id}:{sent_at.timestamp()}"


async def send_invite_email(
    invite_id: int,
    message: OutboundEmail,
    expires_at: datetime | None = None,
    *,
    sent_at: datetime | None,
) -> bool:
    """``sent_at`` is the invite's ``last_sent_at``: which send this is.

    None only for an invitation with no recorded send occasion, which is
    queued every time, as it always was.
    """
    if not is_mail_enabled():
        logger.warning("Invite %s created but SMTP is off; no email sent", invite_id)
        return False
    if sent_at is not None:
        message = replace(message, idempotency_key=invite_mail_key(invite_id, sent_at))
    try:
        await enqueue_email(message, kind="invite", ref=invite_id, not_after=expires_at)
    except MailQueueUnavailableError as exc:
        logger.error("Invite %s email not queued: %s", invite_id, exc)
        return False
    return True
