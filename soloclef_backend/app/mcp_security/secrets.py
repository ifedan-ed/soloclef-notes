"""MCP credentials are write-only through the API.

Reads show every header and env value, and any key-like URL query value, as
``••••`` plus the last four characters. An update that sends a masked value
back keeps the stored one, so a form can round-trip a connector without ever
holding its key. A masked value is only honoured for the host it was stored
for: moving a connector to another server needs the key typed again.
Server-side readers (the research web-API key lookup) read the database row
and never see the masked form.
"""

from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

MASK = "••••"
_SENSITIVE_PARAM = re.compile(r"key|token|secret|auth|pass|sig|cred", re.IGNORECASE)


class MaskedSecretError(ValueError):
    """A masked value was sent back where no stored value can stand in."""


def mask_value(value: Any) -> str:
    text = value if isinstance(value, str) else str(value)
    if len(text) >= 12:
        return MASK + text[-4:]
    return MASK


def is_masked(value: Any) -> bool:
    return isinstance(value, str) and value.startswith(MASK)


def _mask_url(url: str) -> str:
    parts = urlsplit(url)
    if not parts.query:
        return url
    query = [
        (name, mask_value(value) if _SENSITIVE_PARAM.search(name) else value)
        for name, value in parse_qsl(parts.query, keep_blank_values=True)
    ]
    return urlunsplit(parts._replace(query=urlencode(query, safe=MASK)))


def mask_server_config(server_config: Mapping[str, Any] | None) -> dict[str, Any]:
    """A copy safe to return to any reader."""
    cfg = dict(server_config or {})
    headers = cfg.get("headers")
    if isinstance(headers, Mapping):
        cfg["headers"] = {str(k): mask_value(v) for k, v in headers.items()}
    env = cfg.get("env")
    if isinstance(env, Mapping):
        cfg["env"] = {str(k): mask_value(v) for k, v in env.items()}
    url = cfg.get("url")
    if isinstance(url, str):
        cfg["url"] = _mask_url(url)
    return cfg


def mask_connector_config(config: Mapping[str, Any] | None) -> dict[str, Any] | None:
    """A connector config with its ``server_config`` masked, if it has one."""
    if config is None:
        return None
    out = dict(config)
    if isinstance(out.get("server_config"), Mapping):
        out["server_config"] = mask_server_config(out["server_config"])
    return out


def _host(url: Any) -> str | None:
    if not isinstance(url, str):
        return None
    try:
        return (urlsplit(url).hostname or "").lower() or None
    except ValueError:
        return None


def restore_masked(
    incoming: Mapping[str, Any], stored: Mapping[str, Any] | None
) -> dict[str, Any]:
    """``incoming`` with every masked value replaced by the stored one."""
    stored = stored or {}
    out = dict(incoming)
    same_host = _host(incoming.get("url")) == _host(stored.get("url"))

    headers = incoming.get("headers")
    if isinstance(headers, Mapping):
        stored_headers = stored.get("headers")
        stored_headers = stored_headers if isinstance(stored_headers, Mapping) else {}
        by_lower = {str(k).lower(): v for k, v in stored_headers.items()}
        merged: dict[str, Any] = {}
        for name, value in headers.items():
            if is_masked(value):
                if not same_host or str(name).lower() not in by_lower:
                    raise MaskedSecretError(
                        f"Enter the value for the '{name}' header again"
                    )
                merged[name] = by_lower[str(name).lower()]
            else:
                merged[name] = value
        out["headers"] = merged

    url = incoming.get("url")
    if isinstance(url, str) and any(
        is_masked(value)
        for _name, value in parse_qsl(urlsplit(url).query, keep_blank_values=True)
    ):
        stored_url = stored.get("url")
        if not same_host or not isinstance(stored_url, str):
            raise MaskedSecretError("Enter the key in the server URL again")
        stored_params: dict[str, str] = {}
        for name, value in parse_qsl(
            urlsplit(stored_url).query, keep_blank_values=True
        ):
            stored_params.setdefault(name, value)
        parts = urlsplit(url)
        query = []
        for name, value in parse_qsl(parts.query, keep_blank_values=True):
            if is_masked(value):
                if name not in stored_params:
                    raise MaskedSecretError(f"Enter the '{name}' URL value again")
                value = stored_params[name]
            query.append((name, value))
        out["url"] = urlunsplit(parts._replace(query=urlencode(query)))
    return out
