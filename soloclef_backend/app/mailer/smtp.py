"""The SMTP sender.

SMTP is the interface every transactional provider exposes, so the vendor is
configuration (host/port/username/password), not code. ``smtp2go_api.py`` is
the other transport, chosen by ``MAIL_TRANSPORT``.

Stdlib (``email.message`` + ``smtplib``) run through ``asyncio.to_thread``.
Each instance keeps one session open across sends (``smtp_connection.py``):
one instance per mail sender, used for one message at a time.
"""

from __future__ import annotations

import asyncio
import logging
import smtplib
from email.message import EmailMessage

from app.config import config

from .protocol import (
    MailerMisconfiguredError,
    MailerRejectedError,
    MailerUnavailableError,
    OutboundEmail,
)
from .smtp_connection import SmtpConnection

logger = logging.getLogger(__name__)

_LOOPBACK_HOSTS = {"localhost", "127.0.0.1", "::1", "[::1]"}


def _build_mime(message: OutboundEmail, *, sender: str) -> EmailMessage:
    mime = EmailMessage()
    mime["From"] = sender
    mime["To"] = message.to
    mime["Subject"] = message.subject
    if message.reply_to:
        mime["Reply-To"] = message.reply_to

    mime.set_content(message.text_body)
    if message.html_body:
        mime.add_alternative(message.html_body, subtype="html")

    for attachment in message.attachments:
        maintype, _, subtype = attachment.content_type.partition("/")
        mime.add_attachment(
            attachment.content,
            maintype=maintype or "application",
            subtype=subtype or "octet-stream",
            filename=attachment.filename,
        )
    return mime


class SmtpMailer:
    """Sends over SMTP, translating every failure into the port's two errors."""

    def __init__(self, connection: SmtpConnection | None = None) -> None:
        if not config.SMTP_HOST:
            raise ValueError("SMTP_HOST is required when SMTP_ENABLED is true")
        # The default sender is mandatory even though features may override it:
        # a deployment with no From at all should fail at startup, not at the
        # first send.
        if not config.SMTP_FROM:
            raise ValueError("SMTP_FROM is required when SMTP_ENABLED is true")

        security = config.SMTP_SECURITY
        if security not in {"starttls", "tls", "none"}:
            raise ValueError(
                f"Unknown SMTP_SECURITY {security!r}. "
                "Expected one of: starttls, tls, none"
            )
        # Explicit rather than inferred from the port: 465 is implicit TLS and
        # 587 is STARTTLS, and guessing from the port number is the classic
        # source of "it hangs forever with no error".
        if security == "none" and config.SMTP_USERNAME:
            raise ValueError(
                "SMTP_SECURITY=none refuses to send credentials in the clear. "
                "Use starttls or tls, or drop the username for a local relay."
            )
        if security == "none" and config.SMTP_HOST not in _LOOPBACK_HOSTS:
            logger.warning(
                "SMTP_SECURITY=none to non-loopback host %s: mail will cross "
                "the network unencrypted.",
                config.SMTP_HOST,
            )
        self._connection = connection or SmtpConnection()

    def _send_blocking(self, mime: EmailMessage) -> None:
        self._connection.send(mime)

    async def close(self) -> None:
        await asyncio.to_thread(self._connection.close)

    async def send(self, message: OutboundEmail) -> None:
        sender = message.sender or config.SMTP_FROM
        mime = _build_mime(message, sender=sender)
        try:
            await asyncio.to_thread(self._send_blocking, mime)
        except smtplib.SMTPAuthenticationError as exc:
            # Our misconfiguration, not the recipient's address. Never surface
            # this as a rejected email. 454 is the server's own passing trouble.
            if exc.smtp_code < 500:
                raise MailerUnavailableError(
                    f"SMTP authentication temporarily failed ({exc.smtp_code})"
                ) from exc
            raise MailerMisconfiguredError(
                "SMTP authentication failed; check the provider credentials"
            ) from exc
        except smtplib.SMTPSenderRefused as exc:
            # 5xx is almost always an unverified From domain -- our problem;
            # 4xx (e.g. 421 rate limit) passes.
            if exc.smtp_code < 500:
                raise MailerUnavailableError(
                    f"SMTP server temporarily refused the sender ({exc.smtp_code})"
                ) from exc
            raise MailerMisconfiguredError(
                f"SMTP server refused the sender {sender!r}"
            ) from exc
        except smtplib.SMTPRecipientsRefused as exc:
            codes = [code for code, _ in exc.recipients.values()]
            if codes and all(code >= 500 for code in codes):
                raise MailerRejectedError(
                    f"Recipient refused by the mail server: {codes}"
                ) from exc
            raise MailerUnavailableError(
                f"Recipient temporarily refused: {codes}"
            ) from exc
        except smtplib.SMTPResponseException as exc:
            if exc.smtp_code >= 500:
                raise MailerRejectedError(
                    f"Mail server permanently refused the message ({exc.smtp_code})"
                ) from exc
            raise MailerUnavailableError(
                f"Mail server temporarily refused the message ({exc.smtp_code})"
            ) from exc
        except (smtplib.SMTPException, OSError, TimeoutError) as exc:
            # Connect failure, disconnect, DNS, timeout.
            raise MailerUnavailableError(
                f"Could not reach the mail server: {exc}"
            ) from exc
