"""Read-only IMAP client.

Every operation uses ``EXAMINE`` (IMAP's read-only mailbox-select mode).
RFC 3501 says a server should then reject ``STORE``/``EXPUNGE``/``COPY``/
``APPEND``, but not every server actually enforces that (observed against
GreenMail in the integration tests) — so the real guarantee is that this
class fetches message bodies only with ``BODY.PEEK[...]`` (never setting
``\\Seen`` as a side effect) and has no send/delete/move/flag method at all,
regardless of what the server would or wouldn't allow.
"""

from __future__ import annotations

import contextlib
import email
import imaplib
import ipaddress
import logging
import re
import socket
from dataclasses import dataclass, field
from datetime import date
from email.header import decode_header
from email.message import Message
from email.utils import parsedate_to_datetime
from typing import Any

from app.utils.crawl.net_guard import is_public_address

logger = logging.getLogger(__name__)

CONNECT_TIMEOUT_SECONDS = 10
MAX_SEARCH_RESULTS = 50
MAX_FOLDER_LIST = 200
MAX_BODY_CHARS = 20_000

# One generic message for every way a connection attempt can fail before
# login (refused, timed out, TLS handshake failed, or the destination was
# refused by the SSRF guard below). Distinct messages per cause would let
# this endpoint be used to port-scan the backend's network namespace: an
# attacker who can tell "connection refused" apart from "timed out" apart
# from "destination not allowed" learns which internal ports are open.
_REFUSED_MESSAGE = "Could not connect to this mail server"

# Characters that would let a folder name or search argument break out of
# its position on the IMAP command line and smuggle a second command, or a
# line imaplib would otherwise treat as a continuation/response line.
_FORBIDDEN_CHARS = ("\r", "\n", "\x00")


class ImapConnectionError(Exception):
    """Could not connect, log in, or EXAMINE a mailbox."""


class ImapDestinationRefusedError(ImapConnectionError):
    """The configured host does not resolve to a publicly routable address."""


class InvalidImapArgumentError(ImapConnectionError):
    """A folder name or search argument is not safe to send over the wire."""


def _reject_control_chars(value: str, what: str) -> str:
    if any(ch in value for ch in _FORBIDDEN_CHARS):
        raise InvalidImapArgumentError(
            f"{what} must not contain line breaks or null bytes"
        )
    return value


def _resolve_pinned_address(host: str) -> str:
    """One address ``host`` resolves to, proven public — resolve-then-pin.

    Mirrors ``app.mcp_security.url_guard.resolve_public`` (same resolve-then-
    pin shape, this module's version is just synchronous: imaplib itself is
    fully blocking). Every address this name's DNS record answers with is
    resolved up front and checked with
    :func:`app.utils.crawl.net_guard.is_public_address` — the same guard the
    crawler's SSRF check and the MCP destination guard both use — which
    rejects loopback, RFC1918, link-local (including the
    169.254.169.254 cloud metadata address), carrier-grade NAT
    (100.64.0.0/10), IPv6 unique-local, multicast and unspecified addresses.
    An internal Docker service name (``db``, ``redis``, ``mcp``, ``litellm``,
    ...) has no public DNS record of its own and resolves to a private
    container address, so it is refused by this same check without a
    separate "no dots" name rule.

    The connection then targets the address pinned here, not whatever the
    resolver would answer a second time, so a name made to answer
    differently between this check and the connect (DNS rebinding) cannot
    land on a private address. Any failure here — unresolvable, private, or
    a malformed literal — raises :class:`ImapDestinationRefusedError` with
    the same generic message as a refused TCP connect, so this cannot be
    used to distinguish "not allowed" from "nothing there".
    """
    try:
        address = ipaddress.ip_address(host)
    except ValueError:
        pass
    else:
        if not is_public_address(host):
            raise ImapDestinationRefusedError(_REFUSED_MESSAGE)
        return str(address)
    try:
        infos = socket.getaddrinfo(host, None, proto=socket.IPPROTO_TCP)
    except (OSError, UnicodeError) as exc:
        raise ImapDestinationRefusedError(_REFUSED_MESSAGE) from exc
    # IPv4 first: the backend container may have no IPv6 route.
    addresses = sorted({info[4][0] for info in infos}, key=lambda a: ":" in a)
    if not addresses or not all(is_public_address(a) for a in addresses):
        raise ImapDestinationRefusedError(_REFUSED_MESSAGE)
    return addresses[0]


@dataclass(frozen=True)
class ImapAccountSettings:
    host: str
    port: int
    use_tls: bool
    username: str
    # Secrets stay out of repr() so they never reach a log or a traceback.
    password: str = field(repr=False)
    #: Set for a mailbox connected through OAuth (Microsoft): sign in with
    #: SASL XOAUTH2 and this access token instead of LOGIN with ``password``.
    oauth_access_token: str | None = field(default=None, repr=False)


# Shown when the server turns down an OAuth access token. Exchange Online
# answers only "AUTHENTICATE failed." whether the token is wrong or IMAP is
# switched off for the mailbox, and a freshly refreshed token is never wrong.
_TOKEN_REFUSED_MESSAGE = (
    "The mail server refused access to this mailbox ({reason}). "
    "IMAP may be turned off for it."
)


def _xoauth2_answer(username: str, access_token: str):
    """imaplib's ``authenticate`` callback for SASL XOAUTH2.

    The first challenge gets ``user=...^Aauth=Bearer ...^A^A``. A second one
    is the server's error details; answering ``None`` makes imaplib abort,
    so the tagged ``NO`` surfaces as the error.
    """
    for value, what in ((username, "Username"), (access_token, "Access token")):
        _reject_control_chars(value, what)
        if "\x01" in value:
            raise InvalidImapArgumentError(
                f"{what} must not contain control characters"
            )
    initial = f"user={username}\x01auth=Bearer {access_token}\x01\x01".encode()
    answered = False

    def answer(_challenge: bytes) -> bytes | None:
        nonlocal answered
        if answered:
            return None
        answered = True
        return initial

    return answer


def _decode(value: str | None) -> str:
    if not value:
        return ""
    parts = decode_header(value)
    out = []
    for text, enc in parts:
        if isinstance(text, bytes):
            out.append(text.decode(enc or "utf-8", errors="replace"))
        else:
            out.append(text)
    return "".join(out)


def _imap_date(d: date) -> str:
    return d.strftime("%d-%b-%Y")


# RFC 3501 LIST response: ``(flags) "delim"|delim name`` — the mailbox name
# is the remainder after the flag list and the delimiter (quoted-string or
# atom), which may itself be a quoted string or a bare atom. Captures
# whichever one it is; quote-stripping and escape-unquoting happen in
# _parse_list_response_name.
_LIST_RESPONSE_RE = re.compile(r'^\([^)]*\)\s+(?:"(?:[^"\\]|\\.)*"|\S+)\s+(.+)$')


def _parse_list_response_name(text: str) -> str:
    """The mailbox name out of one raw ``LIST`` response line.

    A prior version of this split on the last two ``"`` characters in the
    line, which only worked when the server left the name unquoted; GreenMail
    (and most real servers) quote it (``() "." "INBOX"``), and splitting on
    quotes there picks the empty string *after* the closing quote instead of
    the name between the quotes — silently returning ``""`` for every
    folder. This matches the whole response's shape instead.
    """
    match = _LIST_RESPONSE_RE.match(text.strip())
    raw_name = match.group(1).strip() if match else text.strip()
    if len(raw_name) >= 2 and raw_name[0] == '"' and raw_name[-1] == '"':
        inner = raw_name[1:-1]
        return inner.replace('\\"', '"').replace("\\\\", "\\")
    return raw_name


class _PinnedIMAP4(imaplib.IMAP4):
    """``imaplib.IMAP4`` that connects to a pre-resolved, pre-checked address.

    ``self.host`` stays the original hostname (used for STARTTLS's own
    hostname check); only the raw TCP connect targets the address
    ``_resolve_pinned_address`` already proved public, so the name cannot
    resolve to something else between the check and the connect.
    """

    def __init__(self, host: str, port: int, pinned_address: str, timeout: float):
        self._pinned_address = pinned_address
        super().__init__(host, port, timeout)

    def _create_socket(self, timeout):
        address = (self._pinned_address, self.port)
        if timeout is not None:
            return socket.create_connection(address, timeout)
        return socket.create_connection(address)


class _PinnedIMAP4_SSL(imaplib.IMAP4_SSL):  # noqa: N801 - matches imaplib.IMAP4_SSL's own name
    """As :class:`_PinnedIMAP4`, for the implicit-TLS client.

    ``self.host`` (the original name, never the pinned address) is still
    what TLS verifies the certificate and SNI against — only the TCP
    connect is pinned to the checked address.
    """

    def __init__(self, host: str, port: int, pinned_address: str, timeout: float):
        self._pinned_address = pinned_address
        super().__init__(host=host, port=port, timeout=timeout)

    def _create_socket(self, timeout):
        address = (self._pinned_address, self.port)
        sock = (
            socket.create_connection(address, timeout)
            if timeout is not None
            else socket.create_connection(address)
        )
        return self.ssl_context.wrap_socket(sock, server_hostname=self.host)


class ReadOnlyImapClient:
    """A single short-lived, read-only connection to one IMAP account.

    Use as a context manager; the connection is closed (``LOGOUT``) on exit.
    Never call ``.sock`` / raw ``imap`` methods other than what this class
    exposes — that is the whole point of the wrapper.
    """

    def __init__(self, settings: ImapAccountSettings):
        self._settings = settings
        self._imap: imaplib.IMAP4 | None = None

    def __enter__(self) -> ReadOnlyImapClient:
        s = self._settings
        token_answer = (
            _xoauth2_answer(s.username, s.oauth_access_token)
            if s.oauth_access_token
            else None
        )
        try:
            pinned_address = _resolve_pinned_address(s.host)
            if s.use_tls:
                self._imap = _PinnedIMAP4_SSL(
                    s.host, s.port, pinned_address, timeout=CONNECT_TIMEOUT_SECONDS
                )
            else:
                self._imap = _PinnedIMAP4(
                    s.host, s.port, pinned_address, timeout=CONNECT_TIMEOUT_SECONDS
                )
                self._imap.starttls()
        except ImapDestinationRefusedError:
            # Already the fixed, generic message — do not rewrap it.
            raise
        except (TimeoutError, OSError, imaplib.IMAP4.error) as exc:
            logger.info("email_imap connect failed for %s: %s", s.host, exc)
            raise ImapConnectionError(_REFUSED_MESSAGE) from exc
        try:
            if token_answer is not None:
                self._imap.authenticate("XOAUTH2", token_answer)
            else:
                self._imap.login(s.username, s.password)
        except (TimeoutError, OSError, imaplib.IMAP4.error) as exc:
            with contextlib.suppress(Exception):
                self._imap.shutdown()
            self._imap = None
            # Past this point the destination is already a proven-public
            # host we successfully connected to, so a specific login error
            # (bad credentials) is useful feedback, not a scan oracle.
            if token_answer is not None:
                raise ImapConnectionError(
                    _TOKEN_REFUSED_MESSAGE.format(
                        reason=str(exc).strip() or "no reason"
                    )
                ) from exc
            raise ImapConnectionError(str(exc)) from exc
        return self

    def __exit__(self, *exc: Any) -> None:
        if self._imap is not None:
            with contextlib.suppress(Exception):
                self._imap.logout()
            self._imap = None

    def _conn(self) -> imaplib.IMAP4:
        if self._imap is None:
            raise ImapConnectionError("Not connected")
        return self._imap

    def list_folders(self) -> list[str]:
        typ, data = self._conn().list()
        if typ != "OK":
            raise ImapConnectionError("Could not list folders")
        folders: list[str] = []
        for raw in data[:MAX_FOLDER_LIST]:
            if not raw:
                continue
            text = (
                raw.decode("utf-8", errors="replace") if isinstance(raw, bytes) else raw
            )
            folders.append(_parse_list_response_name(text))
        return folders

    def examine(self, folder: str) -> int:
        """Open ``folder`` read-only; returns the message count."""
        _reject_control_chars(folder, "Folder name")
        mailbox = _quote_imap_string(folder)
        typ, data = self._conn().select(mailbox, readonly=True)
        if typ != "OK":
            raise ImapConnectionError(f"Could not open folder '{folder}' read-only")
        try:
            return int(data[0])
        except (TypeError, ValueError, IndexError):
            return 0

    def search_uids(
        self,
        *,
        text: str | None = None,
        from_: str | None = None,
        to: str | None = None,
        subject: str | None = None,
        since: date | None = None,
        before: date | None = None,
        unseen: bool = False,
        limit: int = MAX_SEARCH_RESULTS,
    ) -> list[bytes]:
        # Use HEADER <field> <string> (RFC 3501) rather than the FROM/TO/
        # SUBJECT shorthands — functionally equivalent substring search, but
        # some servers (observed: GreenMail) mishandle the shorthands while
        # HEADER works correctly, and HEADER lets every field share one
        # quoting/escaping path.
        criteria: list[str] = []
        if text:
            _reject_control_chars(text, "Search text")
            criteria += ["TEXT", f'"{_escape(text)}"']
        if from_:
            _reject_control_chars(from_, "From")
            criteria += ["HEADER", "FROM", f'"{_escape(from_)}"']
        if to:
            _reject_control_chars(to, "To")
            criteria += ["HEADER", "TO", f'"{_escape(to)}"']
        if subject:
            _reject_control_chars(subject, "Subject")
            criteria += ["HEADER", "SUBJECT", f'"{_escape(subject)}"']
        if since:
            criteria += ["SINCE", _imap_date(since)]
        if before:
            criteria += ["BEFORE", _imap_date(before)]
        if unseen:
            # Messages without the \Seen flag, i.e. unread in the user's mail
            # app. Reading here never sets \Seen (BODY.PEEK), so this stays true.
            criteria.append("UNSEEN")
        if not criteria:
            criteria = ["ALL"]
        typ, data = self._conn().uid("search", None, *criteria)
        if typ != "OK":
            raise ImapConnectionError("Search failed")
        uids = data[0].split() if data and data[0] else []
        # Most-recent first, capped.
        uids = list(reversed(uids))
        return uids[: max(0, min(limit, MAX_SEARCH_RESULTS))]

    def fetch_summary(self, uid: bytes) -> dict[str, Any]:
        typ, data = self._conn().uid(
            "fetch", uid, "(BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT DATE)])"
        )
        if typ != "OK" or not data or data[0] is None:
            raise ImapConnectionError("Could not fetch message headers")
        raw = data[0][1] if isinstance(data[0], tuple) else b""
        msg = email.message_from_bytes(raw)
        return {
            "uid": uid.decode(),
            "from": _decode(msg.get("From")),
            "to": _decode(msg.get("To")),
            "subject": _decode(msg.get("Subject")),
            "date": msg.get("Date") or "",
        }

    def fetch_message(self, uid: bytes) -> Message:
        """The full message, fetched read-only (``BODY.PEEK[]``)."""
        typ, data = self._conn().uid("fetch", uid, "(BODY.PEEK[])")
        if typ != "OK" or not data or data[0] is None:
            raise ImapConnectionError("Could not fetch message")
        raw = data[0][1] if isinstance(data[0], tuple) else b""
        return email.message_from_bytes(raw)


def _escape(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"')


def _quote_imap_string(value: str) -> str:
    """An RFC 3501 quoted string for ``value`` (already checked CR/LF/NUL-free).

    ``imaplib.IMAP4.select``/``examine`` send the mailbox argument as-is —
    unlike ``login`` and a few other commands, they do not quote it for the
    caller — so an unquoted folder name containing a space would be split
    into extra command arguments on the wire. Quoting it ourselves, the same
    way ``imaplib.IMAP4._quote`` does, keeps it one atom regardless of
    content (control characters are rejected separately, before this runs).
    """
    return f'"{_escape(value)}"'


def message_headers(msg: Message) -> dict[str, str]:
    return {
        "from": _decode(msg.get("From")),
        "to": _decode(msg.get("To")),
        "cc": _decode(msg.get("Cc")),
        "subject": _decode(msg.get("Subject")),
        "date": msg.get("Date") or "",
    }


def message_date(msg: Message) -> str:
    raw = msg.get("Date")
    if not raw:
        return ""
    try:
        return parsedate_to_datetime(raw).isoformat()
    except (TypeError, ValueError):
        return raw
