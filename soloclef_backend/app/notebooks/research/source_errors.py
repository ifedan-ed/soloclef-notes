"""A research source that cannot answer right now, with words the user can act on.

``code`` lets the web app show the message in the user's language; ``message``
is the English fallback for API callers and unknown codes.
"""

from __future__ import annotations

from typing import Literal

UnavailableCode = Literal[
    "source_blocked",
    "source_busy",
    "source_not_connected",
    "source_key_rejected",
    "source_quota",
]


class SourceUnavailableError(Exception):
    """Raised by a source when it is blocked or down; shown inline, not as a crash."""

    def __init__(self, message: str, code: UnavailableCode = "source_busy") -> None:
        super().__init__(message)
        self.message = message
        self.code = code
