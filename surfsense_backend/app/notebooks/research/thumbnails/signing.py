"""Sign the image URLs we hand out, so the proxy fetches only those."""

from __future__ import annotations

import base64
import hashlib
import hmac

from app.config import config

_PURPOSE = b"notebook-research-thumbnail:"


def _key() -> bytes:
    secret = (config.SECRET_KEY or "").encode()
    return hashlib.sha256(_PURPOSE + secret).digest()


def signature(url: str) -> str:
    return hmac.new(_key(), url.encode(), hashlib.sha256).hexdigest()[:32]


def is_signed(url: str, candidate: str) -> bool:
    return hmac.compare_digest(signature(url), candidate or "")


def encode_url(url: str) -> str:
    return base64.urlsafe_b64encode(url.encode()).decode().rstrip("=")


def decode_url(token: str) -> str | None:
    try:
        padded = token + "=" * (-len(token) % 4)
        return base64.urlsafe_b64decode(padded.encode()).decode()
    except (ValueError, UnicodeDecodeError):
        return None
