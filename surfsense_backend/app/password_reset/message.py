"""The password-reset email: who it is from, what it says, where the link goes."""

from __future__ import annotations

from email.utils import formataddr, parseaddr
from html import escape

from app.brand import BRAND_NAME, BRAND_SITE_URL
from app.config import config
from app.mailer.protocol import OutboundEmail

RESET_PATH = "/reset-password"


def account_sender() -> str | None:
    """``SMTP_ACCOUNT_FROM``, else the brand name on ``SMTP_FROM``."""
    if config.SMTP_ACCOUNT_FROM:
        return config.SMTP_ACCOUNT_FROM
    name, address = parseaddr(config.SMTP_FROM or "")
    if not address:
        return None
    return formataddr((name or BRAND_NAME, address))


def reset_link(token: str) -> str:
    """The page that sets the new password.

    The token rides in the fragment, which browsers never send to a server,
    so it stays out of access logs and Referer headers.
    """
    base = (config.NEXT_FRONTEND_URL or BRAND_SITE_URL).rstrip("/")
    return f"{base}{RESET_PATH}#token={token}"


def build_reset_email(*, to: str, token: str) -> OutboundEmail:
    link = reset_link(token)
    minutes = max(1, config.PASSWORD_RESET_TOKEN_LIFETIME_SECONDS // 60)
    subject = f"Reset your {BRAND_NAME} password"
    text = (
        f"Someone asked to reset the password for your {BRAND_NAME} account "
        f"({to}).\n\n"
        f"To choose a new password, open this link within {minutes} minutes:\n\n"
        f"{link}\n\n"
        "The link works once. If you did not ask for this, ignore this email: "
        "your password stays as it is.\n"
    )
    html = (
        '<div style="font-family:system-ui,-apple-system,Segoe UI,sans-serif;'
        'font-size:15px;line-height:1.5;color:#1f2937;max-width:520px">'
        f"<p>Someone asked to reset the password for your {escape(BRAND_NAME)} "
        f"account ({escape(to)}).</p>"
        f'<p><a href="{escape(link)}" style="display:inline-block;padding:10px 18px;'
        'background:#2f6fed;color:#ffffff;border-radius:6px;text-decoration:none">'
        "Choose a new password</a></p>"
        f"<p>The link works once and expires in {minutes} minutes. If the button "
        f"does not open, copy this address into your browser:<br>"
        f'<span style="word-break:break-all;color:#4b5563">{escape(link)}</span></p>'
        "<p>If you did not ask for this, ignore this email: your password stays "
        "as it is.</p></div>"
    )
    return OutboundEmail(
        to=to,
        subject=subject,
        text_body=text,
        html_body=html,
        sender=account_sender(),
    )
