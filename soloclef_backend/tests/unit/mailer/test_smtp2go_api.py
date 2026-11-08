"""The smtp2go API sender: what it posts, and how each answer maps to an error."""

from __future__ import annotations

import base64
import json

import httpx
import pytest

from app.config import config
from app.mailer.protocol import (
    Attachment,
    MailerMisconfiguredError,
    MailerRejectedError,
    MailerUnavailableError,
    OutboundEmail,
)
from app.mailer.smtp2go_api import Smtp2goApiMailer

pytestmark = pytest.mark.unit


@pytest.fixture(autouse=True)
def api_config(monkeypatch):
    monkeypatch.setattr(config, "SMTP2GO_API_KEY", "api-test-key")
    monkeypatch.setattr(config, "SMTP_FROM", "notes@example.test")
    monkeypatch.setattr(config, "SMTP_TIMEOUT_SECONDS", 5)


def _message(**overrides) -> OutboundEmail:
    fields = {"to": "a@example.test", "subject": "Hi", "text_body": "text"}
    fields.update(overrides)
    return OutboundEmail(**fields)


def _mailer(handler) -> Smtp2goApiMailer:
    return Smtp2goApiMailer(transport=httpx.MockTransport(handler))


def _ok(request):
    return httpx.Response(200, json={"data": {"succeeded": 1, "failed": 0}})


async def test_posts_the_message_with_the_key_header():
    seen = {}

    def handler(request):
        seen["url"] = str(request.url)
        seen["key"] = request.headers["X-Smtp2go-Api-Key"]
        seen["body"] = json.loads(request.content)
        return _ok(request)

    mailer = _mailer(handler)
    await mailer.send(
        _message(
            html_body="<p>h</p>",
            reply_to="r@example.test",
            attachments=(Attachment(filename="f.txt", content=b"abc"),),
        )
    )
    await mailer.close()

    assert seen["url"] == "https://api.smtp2go.com/v3/email/send"
    assert seen["key"] == "api-test-key"
    body = seen["body"]
    assert body["sender"] == "notes@example.test"
    assert body["to"] == ["a@example.test"]
    assert body["html_body"] == "<p>h</p>"
    assert body["custom_headers"] == [{"header": "Reply-To", "value": "r@example.test"}]
    assert base64.b64decode(body["attachments"][0]["fileblob"]) == b"abc"
    assert "api_key" not in body, "the key travels in the header only"


async def test_a_message_sender_overrides_the_default():
    seen = {}

    def handler(request):
        seen["sender"] = json.loads(request.content)["sender"]
        return _ok(request)

    await _mailer(handler).send(_message(sender="Notes <n@example.test>"))
    assert seen["sender"] == "Notes <n@example.test>"


@pytest.mark.parametrize(
    ("status", "body", "expected"),
    [
        (429, {}, MailerUnavailableError),
        (503, {}, MailerUnavailableError),
        (
            401,
            {"data": {"error_code": "E_ApiResponseCodes.API_KEY_INVALID"}},
            MailerMisconfiguredError,
        ),
        (403, {}, MailerMisconfiguredError),
        (
            400,
            {"data": {"error_code": "E_ApiResponseCodes.INVALID_RECIPIENT"}},
            MailerRejectedError,
        ),
        (200, {"data": {"succeeded": 0, "failed": 1}}, MailerRejectedError),
    ],
)
async def test_each_answer_maps_to_one_error(status, body, expected):
    mailer = _mailer(lambda request: httpx.Response(status, json=body))
    with pytest.raises(expected) as caught:
        await mailer.send(_message())
    assert type(caught.value) is expected


async def test_a_network_failure_is_temporary():
    def handler(request):
        raise httpx.ConnectError("refused")

    with pytest.raises(MailerUnavailableError) as caught:
        await _mailer(handler).send(_message())
    assert type(caught.value) is MailerUnavailableError


async def test_the_error_never_quotes_smtp2gos_free_text():
    """smtp2go's error text can name the address; only its code is kept."""
    body = {"data": {"error": "a@example.test is invalid", "error_code": "E_X"}}
    mailer = _mailer(lambda request: httpx.Response(400, json=body))
    with pytest.raises(MailerRejectedError) as caught:
        await mailer.send(_message())
    assert "a@example.test" not in str(caught.value)
    assert "E_X" in str(caught.value)
