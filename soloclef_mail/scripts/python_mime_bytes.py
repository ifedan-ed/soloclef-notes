"""Write the exact bytes smtplib puts in DATA for the app's own MIME builder.

app/mailer/smtp.py's _build_mime is the code under test; app.config is stood in
for because importing the real one loads the embedding stack. The flattening is
smtplib's: BytesGenerator over the message's own policy with CRLF line
endings, then the dot-stuffing and the terminating dot that SMTP.data adds.
Nothing opens a socket.

The multipart boundaries are random, so each one is replaced by a counted
placeholder and the Go side is compared after the same substitution.
"""

import base64
import io
import json
import pathlib
import re
import sys
import types
from email.generator import BytesGenerator

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[2] / "soloclef_backend"))

stub = types.ModuleType("app.config")
stub.config = types.SimpleNamespace(
    SMTP_HOST="127.0.0.1", SMTP_PORT=1025, SMTP_SECURITY="none",
    SMTP_USERNAME="", SMTP_PASSWORD="", SMTP_TIMEOUT_SECONDS=10,
    SMTP_FROM="notes@queue.test",
)
sys.modules["app.config"] = stub

from app.mailer.protocol import Attachment, OutboundEmail
from app.mailer.smtp import _build_mime

LONG = ("Your automation “Weekly reading digest for the research "
        "notebook” — 2026-11-08 finished, and this subject is long "
        "enough that a header has to fold it across several lines")

CASES = [
    ("text_only", dict(to="person@queue.test", subject="Reset your password",
                       text_body="Use the link to set a new password.\n"),
     "notes@queue.test"),
    ("text_and_html", dict(to="person@queue.test", subject="You are invited",
                           text_body="Open the link.\n",
                           html_body="<p>Open the <a href=\"x\">link</a>.</p>"),
     "Soloclef Notes <notes@queue.test>"),
    ("reply_to_and_display_name",
     dict(to="person@queue.test", subject="Billing receipt", text_body="Thanks.",
          reply_to="Soloclef Support <support@queue.test>"),
     "Soloclef Notes <notes@queue.test>"),
    ("non_ascii_subject", dict(to="person@queue.test",
                               subject="Résumé — über 漢字",
                               text_body="Plain ascii body.\n"),
     "notes@queue.test"),
    ("non_ascii_display_name",
     dict(to="person@queue.test", subject="Invitation",
          text_body="Plain ascii body.\n"),
     "Soloclef Nötes — Team <notes@queue.test>"),
    ("non_ascii_body", dict(to="person@queue.test", subject="Grüße",
                            text_body="Grüße aus München.\n nbsp here\n"),
     "notes@queue.test"),
    ("emoji_subject", dict(to="person@queue.test", subject="Done \U0001f600 — ok",
                           text_body="x\n"), "notes@queue.test"),
    ("long_subject_folds", dict(to="person@queue.test", subject=LONG,
                                text_body="x\n"), "notes@queue.test"),
    ("long_non_ascii_subject_folds",
     dict(to="person@queue.test", subject="Über " + LONG, text_body="x\n"),
     "notes@queue.test"),
    ("body_starting_with_a_dot",
     dict(to="person@queue.test", subject="dots",
          text_body=".dot first\nsecond\n.\n..double\nlast.\n"), "notes@queue.test"),
    ("body_with_crlf_already",
     dict(to="person@queue.test", subject="crlf",
          text_body="one\r\ntwo\r\n.\r\nthree\n"), "notes@queue.test"),
    ("body_with_a_very_long_line",
     dict(to="person@queue.test", subject="long line",
          text_body="x" * 200 + "\nshort\n"), "notes@queue.test"),
    ("attachment", dict(to="person@queue.test", subject="Your export",
                        text_body="Attached.\n",
                        attachments=[dict(filename="export.txt",
                                          content=b"-----BEGIN FILE-----\nCONTRACT-BYTES\n-----END FILE-----\n",
                                          content_type="application/octet-stream")]),
     "notes@queue.test"),
    ("html_and_two_attachments",
     dict(to="person@queue.test", subject="Your export", text_body="Attached.\n",
          html_body="<p>Attached.</p>",
          attachments=[dict(filename="export é.txt",
                            content=bytes(range(0, 48)) + b"\xff\xfe\x00END",
                            content_type="application/octet-stream"),
                       dict(filename="tiny.png", content=b"\x89PNG\r\n\x1a\n",
                            content_type="image/png")]),
     "notes@queue.test"),
    ("very_long_non_ascii_word_splits_the_encoded_word",
     dict(to="person@queue.test",
          subject="\u6f22" * 60, text_body="x\n"), "notes@queue.test"),
    ("non_ascii_run_longer_than_a_line",
     dict(to="person@queue.test",
          subject="\u00dcber " + "M\u00fcnchen und Z\u00fcrich " * 6,
          text_body="x\n"), "notes@queue.test"),
    # A subject carrying CR or LF is not a case: email.policy.default refuses
    # it when the header is set, so _build_mime raises before anything is sent.
    # mime_refusals_test.go holds that case.
    ("subject_with_tabs_and_double_spaces",
     dict(to="person@queue.test",
          subject="two  spaces\tand a tab \u00e9 end", text_body="x\n"),
     "notes@queue.test"),
    ("many_lines_with_one_long_one",
     dict(to="person@queue.test", subject="many lines",
          text_body=("short line\n" * 20) + "y" * 300 + "\n" + ("tail\n" * 5)),
     "notes@queue.test"),
    ("non_ascii_body_with_a_long_line",
     dict(to="person@queue.test", subject="long accented line",
          text_body="Gr\u00fc\u00dfe " * 40 + "\n" + "short\n"),
     "notes@queue.test"),
    ("body_ending_without_a_newline",
     dict(to="person@queue.test", subject="no trailing newline",
          text_body="one\ntwo"), "notes@queue.test"),
    ("empty_body",
     dict(to="person@queue.test", subject="empty", text_body=""),
     "notes@queue.test"),
    ("body_with_trailing_whitespace_on_a_long_line",
     dict(to="person@queue.test", subject="trailing space",
          text_body="z" * 100 + "   \nnext\n"), "notes@queue.test"),
    ("large_attachment_spans_base64_lines",
     dict(to="person@queue.test", subject="Your export", text_body="Attached.\n",
          attachments=[dict(filename="big.bin",
                            content=bytes(range(256)) * 3,
                            content_type="application/octet-stream")]),
     "notes@queue.test"),
    ("attachment_with_quotes_in_the_name",
     dict(to="person@queue.test", subject="odd name", text_body="x\n",
          attachments=[dict(filename="re\u00e7u 2026.pdf", content=b"%PDF-1.4\n",
                            content_type="application/pdf")]),
     "notes@queue.test"),
    ("html_only_body_is_still_an_alternative",
     dict(to="person@queue.test", subject="html", text_body="plain\n",
          html_body="<p>" + "x" * 200 + "</p>"), "notes@queue.test"),
]


BOUNDARY = re.compile(rb"={15}\d+==")


def normalise(wire):
    """Replace each random boundary with a counted placeholder."""
    seen = {}
    def swap(match):
        value = match.group(0)
        if value not in seen:
            seen[value] = b"BOUNDARY-%d" % len(seen)
        return seen[value]
    return BOUNDARY.sub(swap, wire)


def quote_periods(wire):
    """smtplib._quote_periods: a line that starts with a dot gets another."""
    return re.sub(rb"(?m)^\.", b"..", wire)


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
    mime = _build_mime(message, sender=sender)

    with io.BytesIO() as out:
        BytesGenerator(out).flatten(mime, linesep="\r\n")
        flat = out.getvalue()

    data = quote_periods(flat)
    if not data.endswith(b"\r\n"):
        data += b"\r\n"
    data += b".\r\n"

    out_fields = dict(fields)
    if "attachments" in out_fields:
        out_fields["attachments"] = [
            dict(filename=a["filename"],
                 content_base64=base64.b64encode(a["content"]).decode("ascii"),
                 content_type=a["content_type"])
            for a in out_fields["attachments"]
        ]
    fixtures.append(dict(
        name=name, message=out_fields, sender=sender,
        mime=normalise(flat).decode("utf-8", "surrogateescape"),
        data=normalise(data).decode("utf-8", "surrogateescape"),
    ))

print(json.dumps(fixtures, indent=2, ensure_ascii=False))
