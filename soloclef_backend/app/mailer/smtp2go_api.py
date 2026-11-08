"""The smtp2go HTTP API sender (``MAIL_TRANSPORT=smtp2go_api``).

One HTTPS request per message over a kept-alive client, so there is no SMTP
conversation, STARTTLS or login per send. Needs a send-only API key
(``SMTP2GO_API_KEY``, made in smtp2go > Settings > API Keys).

Failures map onto the same errors as SMTP: 429, 5xx and network trouble pass;
401/403 are our key; any other 4xx is smtp2go refusing this message.
"""

from __future__ import annotations

import base64

import httpx

from app.config import config

from .protocol import (
    MailerMisconfiguredError,
    MailerRejectedError,
    MailerUnavailableError,
    OutboundEmail,
)

API_BASE_URL = "https://api.smtp2go.com/v3"


def _payload(message: OutboundEmail, *, sender: str) -> dict:
    body: dict = {
        "sender": sender,
        "to": [message.to],
        "subject": message.subject,
        "text_body": message.text_body,
    }
    if message.html_body:
        body["html_body"] = message.html_body
    if message.reply_to:
        body["custom_headers"] = [{"header": "Reply-To", "value": message.reply_to}]
    if message.attachments:
        body["attachments"] = [
            {
                "filename": a.filename,
                "fileblob": base64.b64encode(a.content).decode("ascii"),
                "mimetype": a.content_type,
            }
            for a in message.attachments
        ]
    return body


class Smtp2goApiMailer:
    """Sends through smtp2go's ``/email/send``."""

    def __init__(self, transport: httpx.AsyncBaseTransport | None = None) -> None:
        if not config.SMTP2GO_API_KEY:
            raise ValueError(
                "SMTP2GO_API_KEY is required when MAIL_TRANSPORT=smtp2go_api"
            )
        if not config.SMTP_FROM:
            raise ValueError("SMTP_FROM is required when SMTP_ENABLED is true")
        self._client = httpx.AsyncClient(
            base_url=API_BASE_URL,
            timeout=config.SMTP_TIMEOUT_SECONDS,
            headers={"X-Smtp2go-Api-Key": config.SMTP2GO_API_KEY},
            transport=transport,
        )

    async def send(self, message: OutboundEmail) -> None:
        sender = message.sender or config.SMTP_FROM
        try:
            response = await self._client.post(
                "/email/send", json=_payload(message, sender=sender)
            )
        except httpx.HTTPError as exc:
            raise MailerUnavailableError(
                f"Could not reach the smtp2go API: {type(exc).__name__}"
            ) from exc

        status = response.status_code
        if status == 200 and _succeeded(response):
            return
        if status == 429 or status >= 500:
            raise MailerUnavailableError(f"smtp2go API temporarily refused ({status})")
        if status in (401, 403):
            raise MailerMisconfiguredError(
                f"smtp2go API refused the key ({status}); check SMTP2GO_API_KEY"
            )
        raise MailerRejectedError(
            f"smtp2go API refused the message ({status}, {_error_code(response)})"
        )

    async def close(self) -> None:
        await self._client.aclose()


def _succeeded(response: httpx.Response) -> bool:
    try:
        data = response.json().get("data") or {}
    except ValueError:
        return False
    return int(data.get("succeeded") or 0) >= 1 and not data.get("failed")


def _error_code(response: httpx.Response) -> str:
    """smtp2go's error code only; its free text may quote the address."""
    try:
        data = response.json().get("data") or {}
    except ValueError:
        return "no body"
    return str(data.get("error_code") or "no code")
