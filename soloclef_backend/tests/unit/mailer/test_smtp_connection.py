"""The kept-open SMTP session: reused, checked when idle, renewed when full."""

from __future__ import annotations

import smtplib
from email.message import EmailMessage

import pytest

from app.mailer import smtp_connection
from app.mailer.smtp_connection import SmtpConnection

pytestmark = pytest.mark.unit


class _FakeSession:
    def __init__(self, fail_with: BaseException | None = None, noop_code: int = 250):
        self.sent = 0
        self.closed = False
        self.fail_with = fail_with
        self.noop_code = noop_code

    def send_message(self, _mime):
        if self.fail_with is not None:
            raise self.fail_with
        self.sent += 1

    def noop(self):
        if self.noop_code is None:
            raise smtplib.SMTPServerDisconnected("gone")
        return self.noop_code, b"ok"

    def quit(self):
        self.closed = True

    def close(self):
        self.closed = True


class _Clock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now


def _mime() -> EmailMessage:
    mime = EmailMessage()
    mime.set_content("x")
    return mime


@pytest.fixture
def opened():
    return []


@pytest.fixture
def make(opened):
    def _make(**session_kwargs):
        def opener():
            session = _FakeSession(**session_kwargs)
            opened.append(session)
            return session

        clock = _Clock()
        return SmtpConnection(opener=opener, clock=clock), clock

    return _make


def test_one_session_carries_many_messages(make, opened):
    connection, _ = make()
    for _ in range(10):
        connection.send(_mime())
    assert len(opened) == 1
    assert opened[0].sent == 10
    assert connection.sessions_opened == 1


def test_a_full_session_is_closed_and_a_fresh_one_opened(make, opened):
    connection, _ = make()
    for _ in range(smtp_connection.MESSAGES_PER_SESSION + 1):
        connection.send(_mime())
    assert len(opened) == 2
    assert opened[0].closed
    assert opened[1].sent == 1


def test_an_idle_session_still_open_is_kept(make, opened):
    connection, clock = make()
    connection.send(_mime())
    clock.now += smtp_connection.IDLE_CHECK_SECONDS + 1
    connection.send(_mime())
    assert len(opened) == 1


def test_an_idle_session_the_server_dropped_is_reopened(make, opened):
    connection, clock = make(noop_code=None)
    connection.send(_mime())
    clock.now += smtp_connection.IDLE_CHECK_SECONDS + 1
    connection.send(_mime())
    assert len(opened) == 2
    assert opened[1].sent == 1


def test_a_broken_session_is_dropped_and_the_error_raised(make, opened):
    connection, _ = make(fail_with=smtplib.SMTPServerDisconnected("reset"))
    with pytest.raises(smtplib.SMTPServerDisconnected):
        connection.send(_mime())
    assert opened[0].closed
    with pytest.raises(smtplib.SMTPServerDisconnected):
        connection.send(_mime())
    assert len(opened) == 2, "the next send opens a new session"


def test_a_refused_message_leaves_the_session_open(make, opened):
    connection, _ = make(fail_with=smtplib.SMTPResponseException(552, b"too big"))
    with pytest.raises(smtplib.SMTPResponseException):
        connection.send(_mime())
    assert not opened[0].closed
    opened[0].fail_with = None
    connection.send(_mime())
    assert len(opened) == 1


@pytest.mark.parametrize("security", ["starttls", "tls", "none"])
def test_the_security_mode_is_used_alone(monkeypatch, security):
    """587 + starttls upgrades once; nothing tries implicit TLS and then STARTTLS."""
    from app.config import config

    calls = []

    class _Plain:
        def __init__(self, host, port, timeout):
            calls.append(("plain", port))

        def ehlo(self):
            calls.append(("ehlo",))

        def starttls(self, context):
            calls.append(("starttls",))

        def login(self, user, password):
            calls.append(("login",))

    class _Implicit(_Plain):
        def __init__(self, host, port, timeout, context):
            calls.append(("implicit", port))

    monkeypatch.setattr(smtplib, "SMTP", _Plain)
    monkeypatch.setattr(smtplib, "SMTP_SSL", _Implicit)
    monkeypatch.setattr(config, "SMTP_HOST", "smtp.example.test")
    monkeypatch.setattr(config, "SMTP_PORT", 465 if security == "tls" else 587)
    monkeypatch.setattr(config, "SMTP_SECURITY", security)
    monkeypatch.setattr(config, "SMTP_USERNAME", "" if security == "none" else "u")

    smtp_connection.open_session()

    kinds = [c[0] for c in calls]
    if security == "tls":
        assert kinds == ["implicit", "login"]
    elif security == "starttls":
        assert kinds == ["plain", "ehlo", "starttls", "ehlo", "login"]
    else:
        assert kinds == ["plain"]
