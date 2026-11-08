"""One queued email and the facts the senders need about it, as JSON."""

from __future__ import annotations

import base64
import json
import time
import uuid
from dataclasses import dataclass, field, replace

from app.mailer.protocol import Attachment, OutboundEmail


@dataclass(frozen=True, slots=True)
class MailJob:
    message: OutboundEmail
    # What the mail is for ("password_reset", "invite", ...); logged.
    kind: str
    # Which record it belongs to (a user or invite id); logged, never an address.
    ref: str | None = None
    # Unix time after which the link or code in it no longer works.
    not_after: float | None = None
    attempt: int = 0
    id: str = field(default_factory=lambda: uuid.uuid4().hex)
    enqueued_at: float = field(default_factory=time.time)

    def expired(self, now: float) -> bool:
        return self.not_after is not None and now >= self.not_after

    def next_attempt(self) -> MailJob:
        return replace(self, attempt=self.attempt + 1)

    def to_json(self) -> str:
        m = self.message
        return json.dumps(
            {
                "id": self.id,
                "kind": self.kind,
                "ref": self.ref,
                "not_after": self.not_after,
                "attempt": self.attempt,
                "enqueued_at": self.enqueued_at,
                "message": {
                    "to": m.to,
                    "subject": m.subject,
                    "text_body": m.text_body,
                    "html_body": m.html_body,
                    "sender": m.sender,
                    "reply_to": m.reply_to,
                    "idempotency_key": m.idempotency_key,
                    "attachments": [
                        {
                            "filename": a.filename,
                            "content": base64.b64encode(a.content).decode("ascii"),
                            "content_type": a.content_type,
                        }
                        for a in m.attachments
                    ],
                },
            },
            separators=(",", ":"),
        )

    @classmethod
    def from_json(cls, raw: str | bytes) -> MailJob:
        data = json.loads(raw)
        m = data["message"]
        message = OutboundEmail(
            to=m["to"],
            subject=m["subject"],
            text_body=m["text_body"],
            html_body=m.get("html_body"),
            sender=m.get("sender"),
            reply_to=m.get("reply_to"),
            idempotency_key=m.get("idempotency_key"),
            attachments=tuple(
                Attachment(
                    filename=a["filename"],
                    content=base64.b64decode(a["content"]),
                    content_type=a["content_type"],
                )
                for a in m.get("attachments", [])
            ),
        )
        return cls(
            message=message,
            kind=data["kind"],
            ref=data.get("ref"),
            not_after=data.get("not_after"),
            attempt=int(data.get("attempt", 0)),
            id=data["id"],
            enqueued_at=float(data["enqueued_at"]),
        )
