"""The invitation email: who invited whom, to which workspace, and the link."""

from __future__ import annotations

from datetime import datetime
from html import escape

from app.brand import BRAND_NAME, BRAND_SITE_URL
from app.config import config
from app.mailer.protocol import OutboundEmail
from app.password_reset.message import account_sender

INVITE_PATH = "/invite"


def invite_link(invite_code: str) -> str:
    base = (config.NEXT_FRONTEND_URL or BRAND_SITE_URL).rstrip("/")
    return f"{base}{INVITE_PATH}/{invite_code}"


def build_invite_email(
    *,
    to: str,
    inviter: str,
    workspace_name: str,
    role_name: str,
    invite_code: str,
    expires_at: datetime | None,
) -> OutboundEmail:
    link = invite_link(invite_code)
    expiry = (
        f"The invitation expires on {expires_at.strftime('%d %B %Y')}."
        if expires_at
        else "The invitation does not expire, but it can be withdrawn."
    )
    subject = f"{inviter} invited you to {workspace_name} on {BRAND_NAME}"
    text = (
        f'{inviter} invited you to join the workspace "{workspace_name}" on '
        f"{BRAND_NAME} as {role_name}.\n\n"
        f"To accept, open this link and sign in as {to}:\n\n{link}\n\n"
        f"{expiry} Only the account {to} can accept it. If you were not "
        "expecting this, ignore this email.\n"
    )
    html = (
        '<div style="font-family:system-ui,-apple-system,Segoe UI,sans-serif;'
        'font-size:15px;line-height:1.5;color:#1f2937;max-width:520px">'
        f"<p>{escape(inviter)} invited you to join the workspace "
        f"<strong>{escape(workspace_name)}</strong> on {escape(BRAND_NAME)} as "
        f"{escape(role_name)}.</p>"
        f'<p><a href="{escape(link)}" style="display:inline-block;padding:10px 18px;'
        'background:#2f6fed;color:#ffffff;border-radius:6px;text-decoration:none">'
        "Open the invitation</a></p>"
        f"<p>Sign in as {escape(to)} to accept. {escape(expiry)} If the button "
        "does not open, copy this address into your browser:<br>"
        f'<span style="word-break:break-all;color:#4b5563">{escape(link)}</span></p>'
        "<p>If you were not expecting this, ignore this email.</p></div>"
    )
    return OutboundEmail(
        to=to,
        subject=subject,
        text_body=text,
        html_body=html,
        sender=account_sender(),
    )
