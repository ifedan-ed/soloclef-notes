"""Short handles for signed infographic style selections.

The signed selection token is about 300 characters, and a model had to copy
it into ``execute`` exactly: one wrong character failed the whole job. The
model is now given a short handle (``sel-1a2b3c4d5e``) instead. The full
token stays in the sandbox beside the other infographic state and is looked
up by handle, or by the style id the model names, then verified as before,
so the signature still decides what is trusted. A value that matches nothing
is a retryable tool error that lists the selections this sandbox holds.
"""

from __future__ import annotations

import hashlib
import json
from typing import Any

from app.sandbox import SandboxSession

INDEX_PATH = "/tmp/.surfsense-infographic-selections.json"
HANDLE_PREFIX = "sel-"
_MAX_REMEMBERED = 20


class InfographicSelectionError(ValueError):
    """The model named no selection this sandbox issued; it can try again."""

    def __init__(self, message: str, options: list[dict[str, str]]) -> None:
        super().__init__(message)
        self.options = options

    def as_tool_result(self) -> str:
        return json.dumps(
            {
                "status": "error",
                "retryable": True,
                "error": str(self),
                "valid_selections": self.options,
                "hint": (
                    "Call execute again with infographic_selection_token set to one "
                    "of the handles above, exactly as listed. If none is listed, "
                    "call load_artifact_instructions again to get a new one."
                ),
            },
            sort_keys=True,
        )


def handle_for(token: str) -> str:
    return HANDLE_PREFIX + hashlib.sha256(token.encode()).hexdigest()[:10]


async def _entries(session: SandboxSession) -> list[dict[str, Any]]:
    try:
        raw = await session.read_file(INDEX_PATH)
    except (FileNotFoundError, KeyError):
        return []
    try:
        entries = json.loads(raw.decode())
    except (ValueError, UnicodeDecodeError):
        return []
    if not isinstance(entries, list):
        return []
    return [
        e
        for e in entries
        if isinstance(e, dict)
        and all(isinstance(e.get(k), str) for k in ("handle", "token"))
    ]


async def remember_selection(
    session: SandboxSession,
    token: str,
    *,
    requested_style_id: str,
    resolved_style_id: str,
) -> str:
    """Store the token in the sandbox and return the handle the model copies."""
    handle = handle_for(token)
    entries = [e for e in await _entries(session) if e["handle"] != handle]
    entries.append(
        {
            "handle": handle,
            "token": token,
            "requested_style_id": requested_style_id,
            "resolved_style_id": resolved_style_id,
        }
    )
    await session.write_file(
        INDEX_PATH, json.dumps(entries[-_MAX_REMEMBERED:]).encode()
    )
    return handle


def _options(entries: list[dict[str, Any]]) -> list[dict[str, str]]:
    return [
        {
            "handle": e["handle"],
            "style": str(e.get("resolved_style_id") or ""),
        }
        for e in reversed(entries)
    ]


def _normalise(value: str) -> str:
    return value.strip().strip("`'\"<>").strip()


async def resolve_selection(session: SandboxSession, value: str) -> str:
    """The full signed token for a handle, a style id, or a full token."""
    wanted = _normalise(value)
    entries = await _entries(session)
    if not entries:
        # Nothing remembered: only a full token can be checked.
        return wanted
    newest_first = list(reversed(entries))
    lowered = wanted.lower()
    for entry in newest_first:
        if entry["token"] == wanted or entry["handle"] == lowered:
            return entry["token"]
    if "." in wanted and len(wanted) > 64:
        # A full token not issued here; its signature decides.
        return wanted
    if not lowered.startswith(HANDLE_PREFIX):
        for key in ("resolved_style_id", "requested_style_id"):
            for entry in newest_first:
                if str(entry.get(key) or "").lower() == lowered:
                    return entry["token"]
    raise InfographicSelectionError(
        f"No infographic selection matches {wanted[:40]!r}.", _options(entries)
    )


async def selection_options(session: SandboxSession) -> list[dict[str, str]]:
    return _options(await _entries(session))


__all__ = [
    "InfographicSelectionError",
    "handle_for",
    "remember_selection",
    "resolve_selection",
    "selection_options",
]
