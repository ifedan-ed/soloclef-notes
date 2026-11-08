"""The animated head of a scene has to outlast the stagger helper's motion.

Cutting to the still while elements are still springing in freezes a scene
mid-entrance. The window is a measurement, so it is pinned as one.
"""

from __future__ import annotations

import pytest

from app.deliverables.video.stills_assembly.entrance_window import (
    ENTRANCE_SECONDS,
    entrance_frames,
)

pytestmark = pytest.mark.unit

# The last of six scenes to stop moving in the 2026-11-08 measurement, at 30 fps.
_MEASURED_SETTLE_FRAMES = 105


def test_the_window_outlasts_the_slowest_scene_that_was_measured() -> None:
    assert entrance_frames(30) >= _MEASURED_SETTLE_FRAMES


def test_the_window_outlasts_the_helper_s_own_worst_delay() -> None:
    """``stagger`` delays element i of n by ``i * max(6, fps*2/n)`` frames."""
    fps = 30
    worst_delay = max((items - 1) * max(6, (fps * 2) // items) for items in range(1, 7))
    assert entrance_frames(fps) > worst_delay


def test_the_window_scales_with_the_frame_rate() -> None:
    assert entrance_frames(60) == 2 * entrance_frames(30)
    assert entrance_frames(24) == int(ENTRANCE_SECONDS * 24)


def test_a_frame_rate_of_zero_is_refused() -> None:
    with pytest.raises(ValueError, match="fps"):
        entrance_frames(0)
