"""A hard cap on verify-then-save cycles that end in a refused save.

A Mistral mind-map run verified and failed to save the same file thirteen
times (92 model calls, $3.20) because every save was refused and nothing
stopped the loop. After ``MAX_REFUSED_SAVES`` refusals for one sandbox path
the save raises instead of returning an error to the model, which ends the
run with the reason.
"""

from __future__ import annotations

MAX_REFUSED_SAVES = 3

_refused: dict[tuple[str, str], int] = {}


class SaveAttemptsExhaustedError(RuntimeError):
    """The artifact at this path was refused too many times to keep trying."""


def record_refused_save(session_id: str, path: str, reason: str) -> None:
    """Count a refused save; raise once the cap is reached."""
    key = (session_id, path)
    _refused[key] = _refused.get(key, 0) + 1
    if _refused[key] >= MAX_REFUSED_SAVES:
        count = _refused.pop(key)
        raise SaveAttemptsExhaustedError(
            f"Saving {path} was refused {count} times after verification "
            f"(last reason: {reason}). Stopping instead of verifying again."
        )


def clear_refused_saves(session_id: str, path: str) -> None:
    _refused.pop((session_id, path), None)
