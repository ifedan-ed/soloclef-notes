"""Turn a parsed ``email.message.Message`` into a chat-tool-friendly dict.

HTML bodies are converted to plain text with ``markdownify`` (already a
project dependency used elsewhere for HTML cleanup) rather than returning raw
markup to the model.
"""

from __future__ import annotations

from email.message import Message
from typing import Any

from markdownify import markdownify

from .client import MAX_BODY_CHARS, message_date, message_headers

MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024
# Kept for reference; see app.connectors.email_imap.attachments for the
# actual dispatch (legacy .doc is explicitly unsupported, not listed here).
TEXT_EXTRACTABLE_CONTENT_TYPES = {
    "application/pdf",
    "text/plain",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation",
    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    "application/vnd.ms-excel",
    "text/csv",
    "application/csv",
    "image/png",
    "image/jpeg",
    "image/jpg",
    "image/webp",
    "image/heic",
    "image/heif",
}


def _html_to_text(html: str) -> str:
    try:
        text = markdownify(html, heading_style="ATX")
    except Exception:
        text = html
    # Collapse excess blank lines left over from table/markup conversion.
    lines = [line.rstrip() for line in text.splitlines()]
    out: list[str] = []
    blank = False
    for line in lines:
        if line.strip():
            out.append(line)
            blank = False
        elif not blank:
            out.append("")
            blank = True
    return "\n".join(out).strip()


def extract_body_and_attachments(msg: Message) -> tuple[str, list[dict[str, Any]]]:
    """Returns (plain_text_body, [{filename, content_type, size}])."""
    attachments: list[dict[str, Any]] = []
    text_parts: list[str] = []
    html_parts: list[str] = []

    if msg.is_multipart():
        for part in msg.walk():
            content_type = part.get_content_type()
            disposition = (part.get("Content-Disposition") or "").lower()
            filename = part.get_filename()
            if filename or "attachment" in disposition:
                payload = part.get_payload(decode=True) or b""
                attachments.append(
                    {
                        "filename": filename or "attachment",
                        "content_type": content_type,
                        "size": len(payload),
                    }
                )
                continue
            if content_type == "text/plain":
                payload = part.get_payload(decode=True) or b""
                charset = part.get_content_charset() or "utf-8"
                text_parts.append(payload.decode(charset, errors="replace"))
            elif content_type == "text/html":
                payload = part.get_payload(decode=True) or b""
                charset = part.get_content_charset() or "utf-8"
                html_parts.append(payload.decode(charset, errors="replace"))
    else:
        payload = msg.get_payload(decode=True) or b""
        charset = msg.get_content_charset() or "utf-8"
        decoded = payload.decode(charset, errors="replace")
        if msg.get_content_type() == "text/html":
            html_parts.append(decoded)
        else:
            text_parts.append(decoded)

    body = "\n\n".join(p.strip() for p in text_parts if p.strip())
    if not body and html_parts:
        body = "\n\n".join(_html_to_text(h) for h in html_parts if h.strip())

    if len(body) > MAX_BODY_CHARS:
        body = body[:MAX_BODY_CHARS] + "\n\n[... truncated ...]"

    return body, attachments


def summarize_message(msg: Message, *, uid: str) -> dict[str, Any]:
    body, attachments = extract_body_and_attachments(msg)
    return {
        "uid": uid,
        **message_headers(msg),
        "date_iso": message_date(msg),
        "body": body,
        "attachments": attachments,
    }
