"""Queue the invitation email and say whether the queue took it.

The person inviting sees at once whether the email is on its way, and can
copy the link instead when it is not. The senders drop it unsent if the
invitation has expired by then.
"""

from __future__ import annotations

import logging
from datetime import datetime

from app.mail_queue import MailQueueUnavailableError, enqueue_email
from app.mailer import is_mail_enabled
from app.mailer.protocol import OutboundEmail

logger = logging.getLogger(__name__)


async def send_invite_email(
    invite_id: int, message: OutboundEmail, expires_at: datetime | None = None
) -> bool:
    if not is_mail_enabled():
        logger.warning("Invite %s created but SMTP is off; no email sent", invite_id)
        return False
    try:
        await enqueue_email(message, kind="invite", ref=invite_id, not_after=expires_at)
    except MailQueueUnavailableError as exc:
        logger.error("Invite %s email not queued: %s", invite_id, exc)
        return False
    return True
