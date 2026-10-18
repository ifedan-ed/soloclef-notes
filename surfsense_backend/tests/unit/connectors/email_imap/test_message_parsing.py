from __future__ import annotations

from email.message import EmailMessage

import pytest

from app.connectors.email_imap.message_parsing import (
    extract_body_and_attachments,
    summarize_message,
)

pytestmark = pytest.mark.unit


def test_plain_text_body_is_used_as_is():
    msg = EmailMessage()
    msg["From"] = "a@example.com"
    msg["Subject"] = "Hi"
    msg.set_content("Hello there.")
    body, attachments = extract_body_and_attachments(msg)
    assert "Hello there." in body
    assert attachments == []


def test_html_only_body_is_converted_to_text():
    msg = EmailMessage()
    msg["From"] = "a@example.com"
    msg["Subject"] = "Hi"
    msg.set_content("<html><body><h1>Welcome</h1><p>Some <b>bold</b> text.</p></body></html>", subtype="html")
    body, _ = extract_body_and_attachments(msg)
    assert "<html>" not in body
    assert "<b>" not in body
    assert "Welcome" in body
    assert "bold" in body


def test_attachments_are_listed_not_inlined():
    msg = EmailMessage()
    msg["From"] = "a@example.com"
    msg["Subject"] = "Invoice"
    msg.set_content("See attached.")
    msg.add_attachment(b"%PDF-1.4 fake", maintype="application", subtype="pdf", filename="invoice.pdf")
    body, attachments = extract_body_and_attachments(msg)
    assert "See attached." in body
    assert len(attachments) == 1
    assert attachments[0]["filename"] == "invoice.pdf"
    assert attachments[0]["content_type"] == "application/pdf"
    assert attachments[0]["size"] > 0


def test_summarize_message_includes_headers_and_uid():
    msg = EmailMessage()
    msg["From"] = "prudential@example.com"
    msg["To"] = "me@example.com"
    msg["Subject"] = "Your life insurance policy"
    msg["Date"] = "Mon, 01 Jan 2026 10:00:00 +0000"
    msg.set_content("Policy details enclosed.")
    summary = summarize_message(msg, uid="42")
    assert summary["uid"] == "42"
    assert summary["from"] == "prudential@example.com"
    assert summary["subject"] == "Your life insurance policy"
    assert "Policy details" in summary["body"]
    assert summary["date_iso"].startswith("2026-01-01")


def test_body_is_truncated_past_the_cap():
    msg = EmailMessage()
    msg["From"] = "a@example.com"
    msg["Subject"] = "Long"
    msg.set_content("x" * 30_000)
    body, _ = extract_body_and_attachments(msg)
    assert len(body) < 25_000
    assert body.endswith("[... truncated ...]")
