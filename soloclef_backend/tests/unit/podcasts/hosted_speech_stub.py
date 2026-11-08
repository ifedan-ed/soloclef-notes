"""A loopback stand-in for the proxy's ``/audio/speech`` endpoint.

A real socket server rather than a patched client, because the failures that
matter here are transport behaviour: an endpoint that accepts the request and
then never answers, and one that promises more bytes than it sends. Its audio
is a real encoded MP3 for the same reason -- the adapter decodes what it is
given, so a plausible-looking header would not stand in for one.

Scripted replies are consumed in order and the last one repeats, so a test
states "rate-limited once, then audio" as a list.
"""

from __future__ import annotations

import json
import subprocess
import threading
import time
from dataclasses import dataclass
from functools import lru_cache
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


@lru_cache(maxsize=1)
def speech_mp3() -> bytes:
    """A real, decodable MP3: a short tone standing in for a spoken line."""
    return subprocess.run(
        [
            "ffmpeg",
            "-hide_banner",
            "-loglevel",
            "error",
            "-f",
            "lavfi",
            "-i",
            "sine=frequency=440:duration=0.4",
            "-ac",
            "1",
            "-b:a",
            "64k",
            "-f",
            "mp3",
            "pipe:1",
        ],
        capture_output=True,
        check=True,
    ).stdout


@dataclass(frozen=True, slots=True)
class Reply:
    """One scripted answer: audio, a status code, silence, or a short body."""

    body: bytes
    status: int = 200
    content_type: str = "audio/mpeg"
    # Seconds to withhold the answer; longer than the client's read deadline is
    # how a test reproduces the endpoint hanging.
    delay_seconds: float = 0.0
    # Bytes to promise in Content-Length beyond what is actually written.
    missing_bytes: int = 0


def audio() -> Reply:
    return Reply(body=speech_mp3())


def status(code: int, message: str = "upstream said no") -> Reply:
    return Reply(
        status=code,
        body=json.dumps({"error": {"message": message}}).encode(),
        content_type="application/json",
    )


def hang(delay_seconds: float = 30.0) -> Reply:
    return Reply(body=speech_mp3(), delay_seconds=delay_seconds)


def empty() -> Reply:
    return Reply(body=b"")


def truncated() -> Reply:
    """A body that stops partway through, as a dropped transfer leaves it."""
    whole = speech_mp3()
    return Reply(body=whole[:64], missing_bytes=len(whole) - 64)


def not_audio() -> Reply:
    """A 200 whose body is JSON: an upstream error wearing a success code."""
    return Reply(body=b'{"error":"no audio for you"}', content_type="application/json")


class _Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args) -> None:
        """Keep the test output clean."""

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length)
        stub: HostedSpeechStub = self.server.stub  # type: ignore[attr-defined]
        stub.record(self.path, dict(self.headers), raw)
        reply = stub.next_reply()

        if reply.delay_seconds:
            time.sleep(reply.delay_seconds)

        try:
            self.send_response(reply.status)
            self.send_header("Content-Type", reply.content_type)
            self.send_header(
                "Content-Length", str(len(reply.body) + reply.missing_bytes)
            )
            self.end_headers()
            self.wfile.write(reply.body)
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            # The client gave up first, which is the point of the hang reply.
            self.close_connection = True
            return
        if reply.missing_bytes:
            self.close_connection = True


@dataclass(slots=True)
class SpeechCall:
    """What the client sent for one segment."""

    path: str
    headers: dict[str, str]
    payload: dict


class HostedSpeechStub:
    """A running stub; ``api_base`` is what the adapter should be pointed at."""

    def __init__(self, replies: list[Reply]) -> None:
        if not replies:
            raise ValueError("the stub needs at least one scripted reply")
        self._replies = replies
        self._index = 0
        self.calls: list[SpeechCall] = []
        self._server = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        self._server.daemon_threads = True
        self._server.stub = self  # type: ignore[attr-defined]
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

    @property
    def api_base(self) -> str:
        host, port = self._server.server_address[:2]
        return f"http://{host}:{port}/api/v1"

    def record(self, path: str, headers: dict[str, str], raw: bytes) -> None:
        self.calls.append(
            SpeechCall(
                path=path,
                # Clients are free to choose the case of a header name.
                headers={name.lower(): value for name, value in headers.items()},
                payload=json.loads(raw or b"{}"),
            )
        )

    def next_reply(self) -> Reply:
        reply = self._replies[min(self._index, len(self._replies) - 1)]
        self._index += 1
        return reply

    def __enter__(self) -> HostedSpeechStub:
        self._thread.start()
        return self

    def __exit__(self, *_exc) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=5)
