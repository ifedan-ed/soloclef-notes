"""How long each scene is held, counted in whole frames.

Every later scene's position is the sum of the ones before it, so a scene
rounded the wrong way does not stay a local error: it slides every slide after
it against its narration and clips the last one. The arithmetic therefore works
in exact decimals and whole frames, never in floating-point seconds, and the
rest of the pipeline takes its lengths from here.
"""

from __future__ import annotations

import math
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, replace
from fractions import Fraction
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools.synthesize_narration import (
        NarrationAudio,
    )


@dataclass(frozen=True, slots=True)
class SceneHold:
    """One scene: a rendered entrance, then its slide held to the narration."""

    slide_number: int
    narration_seconds: Fraction
    frames: int
    entrance_frames: int
    start_frame: int
    fps: int

    @property
    def still_frames(self) -> int:
        return self.frames - self.entrance_frames

    @property
    def seconds(self) -> Fraction:
        return Fraction(self.frames, self.fps)

    @property
    def start_seconds(self) -> Fraction:
        return Fraction(self.start_frame, self.fps)


@dataclass(frozen=True, slots=True)
class Timeline:
    """Every scene's place in the finished file."""

    fps: int
    scenes: tuple[SceneHold, ...]

    @property
    def total_frames(self) -> int:
        return sum(scene.frames for scene in self.scenes)

    @property
    def total_seconds(self) -> Fraction:
        return Fraction(self.total_frames, self.fps)


def build_timeline(
    narration: Sequence[NarrationAudio],
    *,
    fps: int,
    min_frames: int,
    entrance_frames: int,
) -> Timeline:
    """Place every narrated scene on the frame grid, in narration order."""
    if not narration:
        raise ValueError("a timeline needs at least one narrated scene")
    if fps <= 0:
        raise ValueError("fps must be positive")
    if min_frames <= 0:
        raise ValueError("min_frames must be positive")
    if entrance_frames <= 0:
        raise ValueError("entrance_frames must be positive")

    scenes: list[SceneHold] = []
    start_frame = 0
    for item in narration:
        seconds = Fraction(str(item["duration_seconds"]))
        if seconds <= 0:
            raise ValueError(f"slide {item['slide_number']} narration must be positive")
        frames = max(min_frames, math.ceil(seconds * fps))
        scenes.append(
            SceneHold(
                slide_number=item["slide_number"],
                narration_seconds=seconds,
                frames=frames,
                entrance_frames=min(entrance_frames, frames),
                start_frame=start_frame,
                fps=fps,
            )
        )
        start_frame += frames
    return Timeline(fps=fps, scenes=tuple(scenes))


def with_rendered_entrances(
    timeline: Timeline, rendered: Mapping[int, Mapping[str, int]]
) -> Timeline:
    """Re-cut the timeline around the entrances Remotion actually rendered.

    The renderer measures each scene's audio itself, so a scene it considers a
    frame shorter than this timeline does renders a shorter entrance than it
    was asked for. The scene keeps its length and its still covers the
    difference; a longer entrance than asked for would push the video past its
    audio and is refused instead.
    """
    scenes = []
    for scene in timeline.scenes:
        if scene.slide_number not in rendered:
            raise ValueError(f"slide {scene.slide_number} was not rendered")
        frames = int(rendered[scene.slide_number]["entrance_frames"])
        if frames <= 0 or frames > scene.entrance_frames:
            raise ValueError(
                f"slide {scene.slide_number} rendered {frames} entrance frames, "
                f"which is not within the {scene.entrance_frames} it was asked for"
            )
        scenes.append(replace(scene, entrance_frames=frames))
    return Timeline(fps=timeline.fps, scenes=tuple(scenes))
