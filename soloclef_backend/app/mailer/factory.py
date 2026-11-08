"""Mailer construction.

``SMTP_ENABLED`` is a deployment choice, not a fallback chain: an unreachable
or unconfigured mail server is an error to fix, never a reason to silently drop
mail. ``MAIL_TRANSPORT`` names the one way mail leaves; it has no default.

Requests never send: they queue (``app/mail_queue``), and each mail sender
builds its own mailer here so it can keep its connection open.
"""

from __future__ import annotations

from app.config import config

from .protocol import Mailer

TRANSPORTS = ("smtp", "smtp2go_api")


def is_mail_enabled() -> bool:
    """Whether mail will actually be delivered.

    Routes that exist only to mail something check this first and refuse,
    rather than reporting a send that never happened.
    """
    return config.SMTP_ENABLED


def build_mailer() -> Mailer:
    """A new sender for ``MAIL_TRANSPORT``. Raises if it is unconfigured."""
    transport = config.MAIL_TRANSPORT
    if transport == "smtp":
        from .smtp import SmtpMailer

        return SmtpMailer()
    if transport == "smtp2go_api":
        from .smtp2go_api import Smtp2goApiMailer

        return Smtp2goApiMailer()
    raise ValueError(
        f"MAIL_TRANSPORT must be one of {', '.join(TRANSPORTS)} "
        f"when SMTP_ENABLED is true (got {transport!r})"
    )
