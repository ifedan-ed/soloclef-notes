"""Where audio/slide drift is born: turning narration seconds into frames.

A still held one frame short of its narration makes every later scene slip
against its voice and truncates the last one, and nothing in the rendered file
says which scene started it. These tests pin the arithmetic at the scene.
"""

from __future__ import annotations

import math
from fractions import Fraction

import pytest

from app.deliverables.video.stills_assembly.scene_timeline import (
    build_timeline,
)

pytestmark = pytest.mark.unit


def _narration(*durations: float) -> list[dict]:
    return [
        {
            "slide_number": index,
            "audio": f"slide-{index}.mp3",
            "duration_seconds": duration,
        }
        for index, duration in enumerate(durations, start=1)
    ]


def _timeline(
    *durations: float, fps: int = 30, min_frames: int = 30, entrance: int = 60
):
    return build_timeline(
        _narration(*durations),
        fps=fps,
        min_frames=min_frames,
        entrance_frames=entrance,
    )


def test_a_scene_is_held_for_its_narration_rounded_up_to_a_whole_frame() -> None:
    timeline = _timeline(7.3)
    assert timeline.scenes[0].frames == 219


def test_rounding_reads_the_duration_as_the_decimal_it_is() -> None:
    """8.3 * 30 is 249.00000000000003 in binary floating point.

    Ceiling that float gives 250: a frame of unasked-for silence, and one
    frame of slip for every scene after it. The arithmetic reads the measured
    duration as the decimal the prober printed instead.
    """
    assert math.ceil(8.3 * 30) == 250
    assert _timeline(8.3).scenes[0].frames == 249


def test_a_fractional_narration_is_never_cut_short() -> None:
    timeline = _timeline(7.31)
    assert timeline.scenes[0].frames == 220
    assert Fraction(220, 30) >= Fraction("7.31")


def test_a_scene_starts_at_the_cumulative_sum_of_the_scenes_before_it() -> None:
    timeline = _timeline(7.3, 2.5, 11.04)
    assert [scene.frames for scene in timeline.scenes] == [219, 75, 332]
    assert [scene.start_frame for scene in timeline.scenes] == [0, 219, 294]
    assert [scene.start_seconds for scene in timeline.scenes] == [
        Fraction(0, 30),
        Fraction(219, 30),
        Fraction(294, 30),
    ]


def test_the_total_is_exactly_the_sum_of_the_scene_holds() -> None:
    timeline = _timeline(22.248, 20.184, 23.208, 18.576, 24.816, 27.864)
    assert timeline.total_frames == sum(scene.frames for scene in timeline.scenes)
    assert timeline.total_seconds == sum(
        (scene.seconds for scene in timeline.scenes), Fraction(0)
    )


def test_the_total_covers_every_narration_and_adds_at_most_a_frame_each() -> None:
    durations = (22.248, 20.184, 23.208, 18.576, 24.816, 27.864)
    timeline = _timeline(*durations)
    narrated = sum(Fraction(str(duration)) for duration in durations)
    assert timeline.total_seconds >= narrated
    assert timeline.total_seconds - narrated < Fraction(len(durations), 30)


def test_a_narration_shorter_than_the_floor_is_still_held_for_the_floor() -> None:
    timeline = _timeline(0.4, fps=30, min_frames=30)
    assert timeline.scenes[0].frames == 30


def test_the_animated_entrance_never_outruns_the_scene_it_opens() -> None:
    timeline = _timeline(1.0, 9.0, entrance=60)
    assert timeline.scenes[0].entrance_frames == 30
    assert timeline.scenes[0].still_frames == 0
    assert timeline.scenes[1].entrance_frames == 60
    assert timeline.scenes[1].still_frames == 210


def test_every_scene_is_its_entrance_plus_its_still_hold() -> None:
    timeline = _timeline(22.248, 0.5, 11.04)
    for scene in timeline.scenes:
        assert scene.entrance_frames + scene.still_frames == scene.frames


def test_narration_order_is_the_order_the_scenes_were_narrated() -> None:
    timeline = _timeline(1.0, 2.0, 3.0)
    assert [scene.slide_number for scene in timeline.scenes] == [1, 2, 3]


def test_an_empty_narration_list_is_refused() -> None:
    with pytest.raises(ValueError, match="at least one"):
        build_timeline([], fps=30, min_frames=30, entrance_frames=60)


def test_a_non_positive_narration_duration_is_refused() -> None:
    with pytest.raises(ValueError, match="positive"):
        _timeline(0.0)
