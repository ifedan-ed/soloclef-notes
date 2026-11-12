"""The service's settings. Each is one environment variable with no default.

A limit nobody chose is a limit nobody knows about, so a missing or malformed
value stops the process at start with the variable's name.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass


class SettingError(ValueError):
    """A required setting is missing or not usable."""


@dataclass(frozen=True)
class Settings:
    #: Address to listen on, as ``host:port``.
    host: str
    port: int
    #: Parser processes kept warm. A parse is CPU-bound and MuPDF is not
    #: thread-safe, so this is also how many files are parsed at once.
    workers: int
    #: Largest request body accepted, in bytes.
    max_bytes: int
    #: Longest one parse may run before its process is killed.
    parse_timeout_seconds: float
    #: Longest a request waits for a free worker before it is told to retry.
    queue_wait_seconds: float


def _required(environ: Mapping[str, str], name: str) -> str:
    value = (environ.get(name) or "").strip()
    if not value:
        raise SettingError(f"{name} is not set")
    return value


def _positive(environ: Mapping[str, str], name: str, kind: type):
    raw = _required(environ, name)
    try:
        value = kind(raw)
    except ValueError as exc:
        raise SettingError(f"{name}={raw!r} is not a number") from exc
    if value <= 0:
        raise SettingError(f"{name}={raw!r} must be greater than zero")
    return value


def load(environ: Mapping[str, str]) -> Settings:
    listen = _required(environ, "PARSE_SERVICE_LISTEN")
    host, separator, port = listen.rpartition(":")
    if not separator or not host or not port.isdigit():
        raise SettingError(f"PARSE_SERVICE_LISTEN={listen!r} is not host:port")
    return Settings(
        host=host,
        port=int(port),
        workers=_positive(environ, "PARSE_SERVICE_WORKERS", int),
        max_bytes=_positive(environ, "PARSE_SERVICE_MAX_BYTES", int),
        parse_timeout_seconds=_positive(
            environ, "PARSE_SERVICE_PARSE_TIMEOUT_SECONDS", float
        ),
        queue_wait_seconds=_positive(
            environ, "PARSE_SERVICE_QUEUE_WAIT_SECONDS", float
        ),
    )
