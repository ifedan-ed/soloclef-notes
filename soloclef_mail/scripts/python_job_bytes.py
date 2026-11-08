"""Write what the app's own MailJob.to_json produces, as Go test fixtures.

Each case names its inputs in the shape the Go test rebuilds them from, so the
comparison is of bytes Python wrote, not of a shape someone agreed on.
"""

import base64
import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from load_real_code import real_modules

protocol, jobmod = real_modules()

CASES = [
    (
        "ascii_text_only",
        dict(to="person@queue.test", subject="Reset your password", text_body="Use the link.\n"),
        dict(kind="password_reset", ref="41", not_after=None, attempt=0,
             id="0123456789abcdef0123456789abcdef", enqueued_at=1762608000.123456),
    ),
    (
        "non_ascii_subject_and_display_name",
        dict(to="rené@queue.test",
             subject="Résumé — über 漢字 \U0001f600",
             text_body="Grüße\n ends with nbsp",
             html_body="<p>Grüße &amp; —</p>",
             sender="Soloclef Nötes <notes@queue.test>",
             reply_to="support@queue.test",
             idempotency_key="idem-é-1"),
        dict(kind="invite", ref=None, not_after=1762608600.0, attempt=0,
             id="ffffffffffffffffffffffffffffffff", enqueued_at=1762608000.0),
    ),
    (
        "attachment_binary",
        dict(to="person@queue.test", subject="Your export", text_body="Attached.",
             attachments=[dict(filename="export é.txt",
                               content=bytes(range(0, 32)) + b"\xff\xfe\x00CONTRACT",
                               content_type="application/octet-stream"),
                          dict(filename="b.bin", content=b"", content_type="image/png")]),
        dict(kind="automation_result", ref="9", not_after=None, attempt=3,
             id="aaaabbbbccccddddeeeeffff00001111", enqueued_at=1762608000.5),
    ),
    (
        "control_characters_and_dot_lines",
        dict(to="person@queue.test",
             subject="tab\there \"quoted\" back\\slash \x01 \x7f",
             text_body=".dot first\r\nsecond\n.\n..double\n",
             html_body=None),
        dict(kind="burst", ref="0", not_after=1e16, attempt=0,
             id="11112222333344445555666677778888", enqueued_at=1.5e-5),
    ),
    (
        "integral_and_extreme_floats",
        dict(to="person@queue.test", subject="floats", text_body="x"),
        dict(kind="floats", ref="r", not_after=1000000000000000.0, attempt=6,
             id="99998888777766665555444433332222", enqueued_at=0.0001),
    ),
]


def encode_input(fields):
    out = dict(fields)
    if "attachments" in out:
        out["attachments"] = [
            dict(filename=a["filename"],
                 content_base64=base64.b64encode(a["content"]).decode("ascii"),
                 content_type=a["content_type"])
            for a in out["attachments"]
        ]
    return out


fixtures = []
for name, message_fields, job_fields in CASES:
    attachments = tuple(
        protocol.Attachment(filename=a["filename"], content=a["content"],
                            content_type=a["content_type"])
        for a in message_fields.get("attachments", [])
    )
    message = protocol.OutboundEmail(
        **{k: v for k, v in message_fields.items() if k != "attachments"},
        attachments=attachments,
    )
    job = jobmod.MailJob(message=message, **job_fields)
    fixtures.append(dict(name=name, message=encode_input(message_fields),
                         job=job_fields, expected=job.to_json()))

print(json.dumps(fixtures, indent=2, ensure_ascii=False))
