"""One SMTP session kept open across sends.

Opening a session costs a TCP connect, the TLS handshake and a login: most of
a send's time at smtp2go. A mail sender (``app/mail_queue``) sends one message
after another, so it keeps its session and reuses it.

A kept session can go stale: the server drops idle sessions and caps how many
messages one may carry. So a session idle for a while is checked with NOOP
before use, and one that has carried ``MESSAGES_PER_SESSION`` is closed and a
new one opened. A NOOP that fails means the server already closed it; that is
a closed connection being reopened, not a second way of sending.

The security mode is ``SMTP_SECURITY`` alone: ``tls`` is implicit TLS from the
first byte (465), ``starttls`` upgrades a plain connect (587). Nothing tries
one mode and then the other.

Blocking (stdlib ``smtplib``); the caller runs it in a thread.
"""

from __future__ import annotations

import smtplib
import ssl
import time
from collections.abc import Callable
from email.message import EmailMessage

from app.config import config

# A session idle longer than this is checked with NOOP before it carries mail.
IDLE_CHECK_SECONDS = 15.0
# Fresh session after this many messages; providers cap messages per session.
MESSAGES_PER_SESSION = 100


def open_session() -> smtplib.SMTP:
    """Connect, secure and log in, exactly as ``SMTP_SECURITY`` says."""
    host = config.SMTP_HOST
    port = config.SMTP_PORT
    timeout = config.SMTP_TIMEOUT_SECONDS
    security = config.SMTP_SECURITY

    if security == "tls":
        client: smtplib.SMTP = smtplib.SMTP_SSL(
            host, port, timeout=timeout, context=ssl.create_default_context()
        )
    else:
        client = smtplib.SMTP(host, port, timeout=timeout)
        if security == "starttls":
            client.ehlo()
            client.starttls(context=ssl.create_default_context())
            client.ehlo()
    try:
        if config.SMTP_USERNAME:
            client.login(config.SMTP_USERNAME, config.SMTP_PASSWORD or "")
    except BaseException:
        _close_quietly(client)
        raise
    return client


def _close_quietly(client: smtplib.SMTP) -> None:
    try:
        client.quit()
    except (smtplib.SMTPException, OSError):
        client.close()


class SmtpConnection:
    """The session one sender reuses. Not shared between threads."""

    def __init__(
        self,
        opener: Callable[[], smtplib.SMTP] = open_session,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self._open = opener
        self._clock = clock
        self._client: smtplib.SMTP | None = None
        self._last_used = 0.0
        self._carried = 0
        # How many sessions this connection has opened; the burst test reads it.
        self.sessions_opened = 0

    def send(self, mime: EmailMessage) -> None:
        client = self._ready_client()
        try:
            client.send_message(mime)
        except (smtplib.SMTPResponseException, smtplib.SMTPRecipientsRefused):
            # The server answered and the session is still in step (smtplib
            # sent RSET after the refusal), so it stays open.
            self._last_used = self._clock()
            raise
        except BaseException:
            # A broken pipe or timeout leaves the session in an unknown state.
            self.close()
            raise
        self._last_used = self._clock()
        self._carried += 1

    def close(self) -> None:
        if self._client is not None:
            _close_quietly(self._client)
        self._client = None
        self._carried = 0

    def _ready_client(self) -> smtplib.SMTP:
        if self._client is not None and self._carried >= MESSAGES_PER_SESSION:
            self.close()
        if (
            self._client is not None
            and self._idle_for() > IDLE_CHECK_SECONDS
            and not self._still_open(self._client)
        ):
            self._client.close()
            self._client = None
            self._carried = 0
        if self._client is None:
            self._client = self._open()
            self.sessions_opened += 1
            self._last_used = self._clock()
        return self._client

    def _idle_for(self) -> float:
        return self._clock() - self._last_used

    @staticmethod
    def _still_open(client: smtplib.SMTP) -> bool:
        try:
            code, _ = client.noop()
        except (smtplib.SMTPException, OSError):
            return False
        return code == 250
