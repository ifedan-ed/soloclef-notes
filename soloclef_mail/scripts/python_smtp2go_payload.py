"""Write the exact request body app/mailer/smtp2go_api.py sends, as fixtures.

The payload comes from the app's own _payload() and is encoded by the httpx the
image has installed, because the encoding is httpx's (ensure_ascii=False,
compact separators, UTF-8) and not something this repository chose. Run inside
a throwaway container of the backend image; it opens no socket and reads no
credential -- the key is a stand-in string and never reaches a request.
"""

import base64
import json
import sys
import types

import httpx

stub = types.ModuleType("app.config")
stub.config = types.SimpleNamespace(
    SMTP2GO_API_KEY="not-a-real-key",
    SMTP_FROM="notes@queue.test",
    SMTP_TIMEOUT_SECONDS=20,
)
sys.modules["app.config"] = stub

from app.mailer.protocol import Attachment, OutboundEmail
from app.mailer.smtp2go_api import _payload

CASES = [
    ("text_only", dict(to="person@queue.test", subject="Reset your password",
                       text_body="Use the link.\n"), "notes@queue.test"),
    ("html_and_reply_to", dict(to="person@queue.test", subject="You are invited",
                               text_body="Open the link.", html_body="<p>a &amp; b</p>",
                               reply_to="support@queue.test"),
     "Soloclef Notes <notes@queue.test>"),
    ("non_ascii_subject_and_display_name",
     dict(to="rené@queue.test",
          subject="Résumé — über 漢字 \U0001f600",
          text_body="Grüße\n ends with nbsp",
          html_body="<p>Grüße &amp; —  </p>",
          reply_to="René <support@queue.test>",
          idempotency_key="idem-1"),
     "Soloclef Nötes <notes@queue.test>"),
    ("long_subject_that_would_need_folding",
     dict(to="person@queue.test",
          subject=("Your automation “Weekly reading digest for the research "
                   "notebook” — 2026-11-08 finished and here is the "
                   "very long line a header would have to fold"),
          text_body="x"),
     "notes@queue.test"),
    ("attachments",
     dict(to="person@queue.test", subject="Your export", text_body="Attached.",
          attachments=[dict(filename="export é.txt",
                            content=bytes(range(0, 32)) + b"\xff\xfe\x00CONTRACT",
                            content_type="application/octet-stream"),
                       dict(filename="b.bin", content=b"", content_type="image/png")]),
     "notes@queue.test"),
    ("body_with_dot_lines_and_crlf",
     dict(to="person@queue.test", subject="dots",
          text_body=".dot first\r\nsecond\n.\n..double\n\ttab"),
     "notes@queue.test"),
]

fixtures = []
for name, fields, sender in CASES:
    attachments = tuple(
        Attachment(filename=a["filename"], content=a["content"],
                   content_type=a["content_type"])
        for a in fields.get("attachments", [])
    )
    message = OutboundEmail(
        **{k: v for k, v in fields.items() if k != "attachments"},
        attachments=attachments,
    )
    request = httpx.Request(
        "POST", "https://api.smtp2go.com/v3/email/send",
        json=_payload(message, sender=sender),
    )
    wire = request.read()
    out = dict(fields)
    if "attachments" in out:
        out["attachments"] = [
            dict(filename=a["filename"],
                 content_base64=base64.b64encode(a["content"]).decode("ascii"),
                 content_type=a["content_type"])
            for a in out["attachments"]
        ]
    fixtures.append(dict(name=name, message=out, sender=sender,
                         expected=wire.decode("utf-8"),
                         content_type=request.headers["content-type"],
                         content_length=request.headers["content-length"]))

print(json.dumps(fixtures, indent=2, ensure_ascii=False))
