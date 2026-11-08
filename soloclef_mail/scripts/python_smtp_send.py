"""Send one case through the app's own SmtpMailer, into a throwaway Mailpit.

Run inside a throwaway container of the backend image. The case arrives on
stdin as JSON, including which Mailpit to send to, so no address or subject is
a shell argument and no real mail server is ever configured here. app.config is
stood in for because the real one loads the embedding stack at import time.
"""

import asyncio
import base64
import json
import sys
import types

case = json.load(sys.stdin)

stub = types.ModuleType("app.config")
stub.config = types.SimpleNamespace(
    SMTP_ENABLED=True,
    SMTP_HOST=case["host"],
    SMTP_PORT=case["port"],
    SMTP_SECURITY="none",
    SMTP_USERNAME="",
    SMTP_PASSWORD="",
    SMTP_TIMEOUT_SECONDS=10,
    SMTP_FROM=case["default_sender"],
)
sys.modules["app.config"] = stub

from app.mailer.protocol import Attachment, OutboundEmail
from app.mailer.smtp import SmtpMailer

message = OutboundEmail(
    to=case["message"]["to"],
    subject=case["message"]["subject"],
    text_body=case["message"]["text_body"],
    html_body=case["message"].get("html_body") or None,
    sender=case.get("sender") or None,
    reply_to=case["message"].get("reply_to") or None,
    attachments=tuple(
        Attachment(
            filename=a["filename"],
            content=base64.b64decode(a["content_base64"]),
            content_type=a["content_type"],
        )
        for a in case["message"].get("attachments", [])
    ),
)


async def main():
    mailer = SmtpMailer()
    try:
        await mailer.send(message)
    finally:
        await mailer.close()


asyncio.run(main())
print("sent")
