"""The automation-result email: subject, plain text, and a simple HTML body.

Reuses the same outgoing mail the account uses for password reset and
verification (``app.mailer``) -- this is just another feature's message
addressed through it, never a sender of its own.
"""

from __future__ import annotations

from datetime import datetime
from html import escape

import markdown as _markdown

from app.mailer.protocol import OutboundEmail
from app.password_reset.message import account_sender


def _render_html(text: str) -> str:
    """Markdown to a small, inline-styled HTML block -- no external CSS.

    ``text`` is escaped first so any literal HTML the agent wrote (it is
    untrusted output) renders as text rather than markup; markdown's own
    syntax characters (``#``, ``*``, backticks, ...) aren't touched by
    ``html.escape`` so formatting still works.
    """
    body = _markdown.markdown(escape(text), extensions=["nl2br"])
    return (
        '<div style="font-family:system-ui,-apple-system,Segoe UI,sans-serif;'
        'font-size:15px;line-height:1.5;color:#1f2937;max-width:640px">'
        f"{body}</div>"
    )


def build_success_email(
    *, to: str, automation_name: str, run_date: datetime, output_text: str | None
) -> OutboundEmail:
    subject = f"{automation_name} — {run_date.strftime('%Y-%m-%d')}"
    text = output_text or "This automation finished with no text output."
    return OutboundEmail(
        to=to,
        subject=subject,
        text_body=text,
        html_body=_render_html(text),
        sender=account_sender(),
    )


# Subject tag and first line of the short email for a run that did not succeed.
# A cancelled run sends none: whoever cancelled it already knows.
_FAILURE_WORDING: dict[str, tuple[str, str]] = {
    "failed": ("failed", "This automation run failed."),
    "needs_attention": ("needs attention", "This automation run needs your attention."),
    "timed_out": (
        "timed out",
        "This automation run hit its time limit and was stopped.",
    ),
}


def build_failure_email(
    *,
    to: str,
    automation_name: str,
    run_date: datetime,
    status: str,
    reason: str | None,
) -> OutboundEmail:
    """``status`` is ``failed``, ``needs_attention`` or ``timed_out``."""
    tag, headline = _FAILURE_WORDING.get(status, _FAILURE_WORDING["failed"])
    subject = f"{automation_name} — {run_date.strftime('%Y-%m-%d')} ({tag})"
    text = f"{headline}\n\n{reason or 'No further detail was reported.'}"
    return OutboundEmail(
        to=to,
        subject=subject,
        text_body=text,
        html_body=_render_html(text),
        sender=account_sender(),
    )
