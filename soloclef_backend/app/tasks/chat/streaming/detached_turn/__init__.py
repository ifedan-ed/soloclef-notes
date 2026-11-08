"""Chat turns that keep running when the page that started them goes away."""

from __future__ import annotations

from .cleanup import run_cleanup_to_the_end
from .registry import (
    STOP_WAIT_SECONDS,
    is_running as is_turn_running,
    is_stopping as is_turn_stopping,
    stop as stop_running_turn,
    wait_until_finished as wait_for_running_turn,
)
from .relay import KEEPALIVE_FRAME, KEEPALIVE_INTERVAL_SECONDS, start_turn_in_background

__all__ = [
    "KEEPALIVE_FRAME",
    "KEEPALIVE_INTERVAL_SECONDS",
    "STOP_WAIT_SECONDS",
    "is_turn_running",
    "is_turn_stopping",
    "run_cleanup_to_the_end",
    "start_turn_in_background",
    "stop_running_turn",
    "wait_for_running_turn",
]
