"""What counts as an importable URL, and when two URLs are the same source."""

from __future__ import annotations

import hashlib
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

_HTTP_SCHEMES = frozenset({"http", "https"})

# Query parameters that only track the click; they never change the page.
_TRACKING_PARAMS = frozenset(
    {"fbclid", "gclid", "dclid", "msclkid", "mc_cid", "mc_eid", "ref", "ref_src"}
)


def is_http_url(value: str | None) -> bool:
    if not value or not isinstance(value, str):
        return False
    parts = urlsplit(value.strip())
    return parts.scheme.lower() in _HTTP_SCHEMES and bool(parts.hostname)


def require_http_url(value: str) -> str:
    """The trimmed URL, or a ValueError a request validator turns into a 422."""
    url = value.strip()
    if len(url) > 2048 or not is_http_url(url):
        raise ValueError("must be an http(s) URL")
    return url


def normalize_url(url: str) -> str:
    """A comparison key: same page even if scheme, case, www, slash or trackers differ."""
    parts = urlsplit(url.strip())
    host = (parts.hostname or "").lower()
    if host.startswith("www."):
        host = host[4:]
    if parts.port and parts.port not in (80, 443):
        host = f"{host}:{parts.port}"
    path = parts.path.rstrip("/") or ""
    query = urlencode(
        sorted(
            (key, value)
            for key, value in parse_qsl(parts.query, keep_blank_values=True)
            if key.lower() not in _TRACKING_PARAMS
            and not key.lower().startswith("utm_")
        )
    )
    return urlunsplit(("", host, path, query, ""))[2:]


def result_id(url: str) -> str:
    """Stable id for a result row, equal for URLs that normalize the same."""
    return hashlib.sha1(normalize_url(url).encode()).hexdigest()
