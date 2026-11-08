"""What a hand-off can be for, and where the Drive Picker goes back to in the app.

Each purpose ends at a fixed address of the app (never one the request
names), so a forged hand-off cannot send anything elsewhere: the Drive
Picker at ``soloclef://drive/done`` (here; the app's side is
``soloclef_mobile/src/features/sources/drive/driveHandoff.ts``), a connector
sign-in at ``APP_HANDOFF_RETURN``, ``soloclef://connectors/done``
(``returns.py``; the app's side is
``soloclef_mobile/src/features/workspace/connectors/handoff.ts``).
"""

from __future__ import annotations

from urllib.parse import urlencode

from .codes import DRIVE_CONNECT, DRIVE_PICKER, MICROSOFT_MAIL, PURPOSES

__all__ = [
    "APP_RETURNS",
    "CONNECTOR_PURPOSES",
    "DRIVE_CONNECT",
    "DRIVE_PICKER",
    "MICROSOFT_MAIL",
    "PLANNED_PURPOSES",
    "PURPOSES",
    "UNSUPPORTED",
    "app_return",
    "is_supported",
]

#: Where the Drive Picker goes back to (the connector sign-ins: returns.py).
APP_RETURNS: dict[str, str] = {
    DRIVE_PICKER: "soloclef://drive/done",
}

#: The sign-ins that come back through their provider's callback (returns.py).
CONNECTOR_PURPOSES = frozenset({DRIVE_CONNECT, MICROSOFT_MAIL})

#: Purposes named but not built yet: none since B10 joined.
PLANNED_PURPOSES: frozenset[str] = frozenset()

UNSUPPORTED = "HANDOFF_PURPOSE_UNSUPPORTED"


def is_supported(purpose: str) -> bool:
    return purpose in PURPOSES


def app_return(purpose: str, **params: str | int) -> str:
    """The app's address for the Drive Picker with ``params`` (count, unsupported, error)."""
    base = APP_RETURNS[purpose]
    query = urlencode({k: str(v) for k, v in params.items() if v is not None})
    return f"{base}?{query}" if query else base
