"""Which way a video deliverable is assembled: one setting, no guessing.

``frames`` is the original path: Remotion screenshots every frame of the
composition and encodes the result. ``stills`` renders each scene's entrance
and then holds one still for the rest of its narration
(``app/deliverables/video/stills_assembly``).

Anything else is refused rather than read as one of them, so a typo in
``VIDEO_RENDER_MODE`` fails the job that set it instead of quietly rendering
the other way.
"""

from __future__ import annotations

from typing import Final

FRAMES: Final = "frames"
STILLS: Final = "stills"
_MODES: Final = (FRAMES, STILLS)


def render_mode(configured: str | None) -> str:
    """The configured mode, named exactly."""
    mode = (configured or "").strip().lower()
    if mode not in _MODES:
        raise ValueError(
            f"VIDEO_RENDER_MODE must be one of {', '.join(_MODES)}; got {configured!r}"
        )
    return mode
