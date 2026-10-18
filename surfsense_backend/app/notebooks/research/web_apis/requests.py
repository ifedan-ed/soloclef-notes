"""One JSON POST to a search API, and what came back."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class ApiRequest:
    url: str
    headers: dict[str, str]
    body: dict[str, Any]


@dataclass(frozen=True)
class ApiReply:
    status: int
    data: Any = None
    headers: dict[str, str] = field(default_factory=dict)


# Sends one request, once: no retries, since every call is billed to the user's key.
ApiSender = Callable[[ApiRequest], Awaitable[ApiReply]]
