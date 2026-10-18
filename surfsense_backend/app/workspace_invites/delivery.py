"""Send the invitation email and say whether it went.

Sent inline, not after the response: the person inviting sees at once whether
the email left, and can copy the link instead when it did not.
"""

from __future__ import annotations

import logging

from app.mailer import MailerError, get_mailer, is_mail_enabled
from app.mailer.protocol import OutboundEmail

logger = logging.getLogger(__name__)


async def send_invite_email(invite_id: int, message: OutboundEmail) -> bool:
    if not is_mail_enabled():
        logger.warning("Invite %s created but SMTP is off; no email sent", invite_id)
        return False
    try:
        await get_mailer().send(message)
    except MailerError as exc:
        logger.error("Invite %s email failed: %s", invite_id, type(exc).__name__)
        return False
    logger.info("Invite %s email sent", invite_id)
    return True
