"""A tiny IMAP server on loopback that only knows SASL XOAUTH2.

Answers the way Exchange Online does (see Microsoft's "Authenticate an IMAP,
POP or SMTP connection using OAuth"): ``AUTHENTICATE XOAUTH2`` gets a ``+``
continuation, the client's base64 line is checked against the one token this
server accepts, and a wrong token gets ``NO AUTHENTICATE failed.``. After a
successful sign-in it lists two folders, enough for a connection test.
"""

from __future__ import annotations

import base64
import socket
import threading
from dataclasses import dataclass, field


@dataclass
class FakeXoauth2ImapServer:
    accepted_user: str
    accepted_token: str
    #: Every decoded XOAUTH2 initial response the server received.
    sign_ins: list[str] = field(default_factory=list)
    #: Every command line (tag stripped) the server received.
    commands: list[str] = field(default_factory=list)

    def __post_init__(self) -> None:
        self._listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self._listener.bind(("127.0.0.1", 0))
        self._listener.listen(4)
        self.port = self._listener.getsockname()[1]
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()

    def close(self) -> None:
        self._listener.close()

    def _serve(self) -> None:
        while True:
            try:
                conn, _ = self._listener.accept()
            except OSError:
                return
            threading.Thread(target=self._session, args=(conn,), daemon=True).start()

    def _session(self, conn: socket.socket) -> None:
        stream = conn.makefile("rwb", buffering=0)

        def send(line: str) -> None:
            stream.write(line.encode() + b"\r\n")

        send("* OK [CAPABILITY IMAP4rev1 AUTH=XOAUTH2] fake Exchange ready")
        signed_in = False
        try:
            while True:
                raw = stream.readline()
                if not raw:
                    return
                tag, _, command = raw.decode().rstrip("\r\n").partition(" ")
                self.commands.append(command)
                verb = command.split(" ", 1)[0].upper()
                if verb == "CAPABILITY":
                    send("* CAPABILITY IMAP4rev1 AUTH=XOAUTH2")
                    send(f"{tag} OK CAPABILITY completed.")
                elif command.upper() == "AUTHENTICATE XOAUTH2":
                    send("+ ")
                    answer = stream.readline().decode().strip()
                    decoded = base64.b64decode(answer).decode()
                    self.sign_ins.append(decoded)
                    expected = (
                        f"user={self.accepted_user}\x01"
                        f"auth=Bearer {self.accepted_token}\x01\x01"
                    )
                    if decoded == expected:
                        signed_in = True
                        send(f"{tag} OK AUTHENTICATE completed.")
                    else:
                        send(f"{tag} NO AUTHENTICATE failed.")
                elif verb == "LOGIN":
                    send(f"{tag} NO LOGIN failed.")
                elif verb == "LIST" and signed_in:
                    send('* LIST (\\HasNoChildren) "/" "INBOX"')
                    send('* LIST (\\HasNoChildren) "/" "Sent Items"')
                    send(f"{tag} OK LIST completed.")
                elif verb == "LOGOUT":
                    send("* BYE Microsoft Exchange Server IMAP4 server signing off.")
                    send(f"{tag} OK LOGOUT completed.")
                    return
                else:
                    send(f"{tag} BAD Command Error. 10")
        finally:
            conn.close()
