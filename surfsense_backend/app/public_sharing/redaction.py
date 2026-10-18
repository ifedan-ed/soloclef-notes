"""Keep what a stranger with the link may see; drop what only the server needs.

Storage keys, server file paths and account ids never leave in a snapshot.
The workspace id stays: the image card needs it and it grants nothing.
"""

from __future__ import annotations

from typing import Any

INTERNAL_KEYS = frozenset(
    {
        "storage_key",
        "audio_storage_key",
        "storage_backend",
        "file_path",
        "audio_file",
        "user_id",
        "created_by_id",
        "author_id",
        "email",
        "user_email",
        "api_key",
        "token",
    }
)


def without_internal_keys(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            key: without_internal_keys(item)
            for key, item in value.items()
            if key not in INTERNAL_KEYS
        }
    if isinstance(value, list):
        return [without_internal_keys(item) for item in value]
    return value


def public_author(display_name: str | None) -> dict | None:
    """Only a display name; no account id, no avatar URL."""
    if display_name is None:
        return None
    return {"display_name": display_name, "avatar_url": None}
