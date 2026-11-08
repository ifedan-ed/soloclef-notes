"""How much of a scene still has to be rendered frame by frame.

A scene's only motion is the baked stagger helper bringing its elements in:
``stagger`` delays element ``index`` of ``total`` by ``index * max(6, fps*2/total)``
frames and then springs it into place. Once the last element has settled the
scene is a static slide, and every frame after that is the same screenshot.

The window below is where that motion ends. Measured on 2026-11-08 against a
six-scene authored presentation rendered the existing way at 30 fps: comparing
each scene's frames with its own settled frame, the last one reached the
encoder's noise floor at frame 105 (scenes 1, 3 and 6 by frame 60-75). Four
seconds carries that worst case with half a second to spare, and it is
expressed in seconds because the helper's delays are fps-relative.

A scene that staggers far more elements than the author prompt's "at most about
six" can still be moving when the window closes; it would hold a slide caught
mid-entrance rather than cut badly.
"""

from __future__ import annotations

import math
from typing import Final

ENTRANCE_SECONDS: Final = 4.0


def entrance_frames(fps: int) -> int:
    """Frames of real animation at the head of every scene."""
    if fps <= 0:
        raise ValueError("fps must be positive")
    return math.ceil(ENTRANCE_SECONDS * fps)
