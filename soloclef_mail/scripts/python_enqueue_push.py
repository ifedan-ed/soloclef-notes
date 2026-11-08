"""Queue one email through the app's own enqueue_email, id and clock pinned.

Run inside a throwaway container of the backend image, pointed at a throwaway
Redis, so the entry on soloclef:mail:ready is one the live code wrote. The case
arrives on stdin as JSON: no address or subject is ever a shell argument.

app.config is stood in for rather than imported: the real one loads the
embedding stack at import time, and the only setting enqueue.py reads is which
Redis to push to. Everything under test -- enqueue.py, job.py, protocol.py --
is the app's own code.

time.time is pinned before job.py is imported, because MailJob binds it as the
enqueued_at default factory at class creation and patching the module
afterwards is too late. not_after arrives as a float: datetime.timestamp() and
time.time() are what the three call sites pass, so a float is the type under
test.
"""

import asyncio
import base64
import json
import sys
import time
import types
import uuid

case = json.load(sys.stdin)

time.time = lambda: case["enqueued_at"]

stub = types.ModuleType("app.config")
stub.config = types.SimpleNamespace(REDIS_APP_URL=case["redis_url"])
sys.modules["app.config"] = stub

from app.mail_queue import enqueue_email
from app.mail_queue import job as jobmod
from app.mailer.protocol import Attachment, OutboundEmail

jobmod.uuid = type("_", (), {"uuid4": staticmethod(lambda: uuid.UUID(hex=case["id"]))})

message = OutboundEmail(
    to=case["message"]["to"],
    subject=case["message"]["subject"],
    text_body=case["message"]["text_body"],
    html_body=case["message"].get("html_body"),
    sender=case["message"].get("sender"),
    reply_to=case["message"].get("reply_to"),
    idempotency_key=case["message"].get("idempotency_key"),
    attachments=tuple(
        Attachment(
            filename=a["filename"],
            content=base64.b64decode(a["content_base64"]),
            content_type=a["content_type"],
        )
        for a in case["message"].get("attachments", [])
    ),
)

print(
    asyncio.run(
        enqueue_email(
            message,
            kind=case["kind"],
            ref=case.get("ref"),
            not_after=None if case.get("not_after") is None else float(case["not_after"]),
        )
    )
)
