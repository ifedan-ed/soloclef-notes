"""Transactional email for the backend.

How mail leaves: SMTP (any provider) or smtp2go's HTTP API, one chosen by
``MAIL_TRANSPORT``. Each feature builds its own messages and stamps its own
sender on them, so nothing here knows what any of them are for. Features
queue their mail (``app/mail_queue``); only the mail senders call a mailer.
"""

from .factory import build_mailer, is_mail_enabled
from .protocol import (
    Attachment,
    Mailer,
    MailerError,
    MailerMisconfiguredError,
    MailerRejectedError,
    MailerUnavailableError,
    OutboundEmail,
)

__all__ = [
    "Attachment",
    "Mailer",
    "MailerError",
    "MailerMisconfiguredError",
    "MailerRejectedError",
    "MailerUnavailableError",
    "OutboundEmail",
    "build_mailer",
    "is_mail_enabled",
]
