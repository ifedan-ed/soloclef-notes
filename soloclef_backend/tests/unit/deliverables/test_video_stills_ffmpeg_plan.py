"""The ffmpeg commands that hold each slide for exactly its narration.

The arithmetic is only half the risk: a command that holds a slide for a
rounded number of seconds instead of a counted number of frames, or that pads
audio to a truncated decimal, reintroduces the drift the timeline removed.
"""

from __future__ import annotations

from pathlib import PurePosixPath

import pytest

from app.deliverables.video.stills_assembly.ffmpeg_plan import (
    AUDIO_SAMPLE_RATE,
    ScenePart,
    plan_assembly,
)
from app.deliverables.video.stills_assembly.scene_timeline import build_timeline

pytestmark = pytest.mark.unit

_WORKDIR = PurePosixPath("/workspace/job-7")
_OUTPUT = "/workspace/job-7.mp4"


def _timeline(*durations: float, fps: int = 30, entrance: int = 60):
    return build_timeline(
        [
            {
                "slide_number": index,
                "audio": f"slide-{index}.mp3",
                "duration_seconds": duration,
            }
            for index, duration in enumerate(durations, start=1)
        ],
        fps=fps,
        min_frames=30,
        entrance_frames=entrance,
    )


def _parts(count: int) -> list[ScenePart]:
    return [
        ScenePart(
            slide_number=index,
            entrance_video=f"{_WORKDIR}/scene-parts/entrance-{index}.mp4",
            still_image=f"{_WORKDIR}/scene-parts/still-{index}.png",
            narration_audio=f"{_WORKDIR}/public/slide-{index}.mp3",
        )
        for index in range(1, count + 1)
    ]


def _plan(*durations: float, fps: int = 30, entrance: int = 60):
    timeline = _timeline(*durations, fps=fps, entrance=entrance)
    return plan_assembly(
        _parts(len(durations)),
        timeline,
        workdir=_WORKDIR,
        output_path=_OUTPUT,
    )


def _command(plan, output: str) -> tuple[str, ...]:
    """The one command that writes ``output``."""
    matches = [command for command in plan.commands if command[-1] == output]
    assert len(matches) == 1, f"{output} is written by {len(matches)} commands"
    return matches[0]


def test_a_still_is_held_for_a_counted_number_of_frames_not_a_duration() -> None:
    plan = _plan(7.3, entrance=60)
    hold = _command(plan, f"{_WORKDIR}/scene-parts/hold-1.mp4")
    assert "-frames:v" in hold
    assert hold[hold.index("-frames:v") + 1] == "159"
    assert "-t" not in hold
    assert hold[hold.index("-framerate") + 1] == "30"


def test_a_held_slide_is_encoded_the_way_the_entrances_already_are() -> None:
    """Concatenation without re-encoding is silent about a mismatch.

    An hour of debugging said so: a hold written with the encoder's default
    pixel format and time scale concatenated cleanly and played a 137-second
    video in 113 seconds.
    """
    hold = _command(_plan(7.3), f"{_WORKDIR}/scene-parts/hold-1.mp4")
    assert hold[hold.index("-pix_fmt") + 1] == "yuvj420p"
    assert hold[hold.index("-color_range") + 1] == "pc"
    assert hold[hold.index("-video_track_timescale") + 1] == "90000"


def test_the_whole_scene_is_its_entrance_then_its_hold_in_playback_order() -> None:
    plan = _plan(7.3, 9.0)
    assert plan.concat_manifest.splitlines() == [
        f"file '{_WORKDIR}/scene-parts/entrance-1.mp4'",
        f"file '{_WORKDIR}/scene-parts/hold-1.mp4'",
        f"file '{_WORKDIR}/scene-parts/entrance-2.mp4'",
        f"file '{_WORKDIR}/scene-parts/hold-2.mp4'",
    ]
    assert plan.concat_manifest_path == f"{_WORKDIR}/scene-parts/video-parts.txt"


def test_a_scene_with_no_room_for_a_still_contributes_only_its_entrance() -> None:
    plan = _plan(1.0, 9.0, entrance=60)
    assert plan.concat_manifest.splitlines() == [
        f"file '{_WORKDIR}/scene-parts/entrance-1.mp4'",
        f"file '{_WORKDIR}/scene-parts/entrance-2.mp4'",
        f"file '{_WORKDIR}/scene-parts/hold-2.mp4'",
    ]
    assert not [
        command
        for command in plan.commands
        if f"{_WORKDIR}/scene-parts/hold-1.mp4" in command
    ]


def test_the_video_parts_are_concatenated_without_re_encoding() -> None:
    plan = _plan(7.3)
    concat = _command(plan, f"{_WORKDIR}/scene-parts/video.mp4")
    assert "-f" in concat and concat[concat.index("-f") + 1] == "concat"
    assert concat[concat.index("-i") + 1] == plan.concat_manifest_path
    assert "-c" in concat and concat[concat.index("-c") + 1] == "copy"


def test_each_narration_is_padded_to_its_scene_in_whole_samples() -> None:
    plan = _plan(7.3, 2.5)
    track = _command(plan, f"{_WORKDIR}/scene-parts/narration.m4a")
    filtergraph = track[track.index("-filter_complex") + 1]
    samples_per_frame = AUDIO_SAMPLE_RATE // 30
    assert f"atrim=end_sample={219 * samples_per_frame}" in filtergraph
    assert f"atrim=end_sample={75 * samples_per_frame}" in filtergraph
    assert "concat=n=2:v=0:a=1" in filtergraph
    assert "apad" in filtergraph


def test_the_narration_track_reads_every_scene_in_order() -> None:
    plan = _plan(7.3, 2.5)
    track = _command(plan, f"{_WORKDIR}/scene-parts/narration.m4a")
    inputs = [track[index + 1] for index, value in enumerate(track) if value == "-i"]
    assert inputs == [
        f"{_WORKDIR}/public/slide-1.mp3",
        f"{_WORKDIR}/public/slide-2.mp3",
    ]


def test_the_padded_track_is_exactly_as_long_as_the_video() -> None:
    plan = _plan(22.248, 20.184, 23.208)
    timeline = _timeline(22.248, 20.184, 23.208)
    track = _command(plan, f"{_WORKDIR}/scene-parts/narration.m4a")
    filtergraph = track[track.index("-filter_complex") + 1]
    samples = sum(
        int(fragment.split("=")[-1])
        for fragment in filtergraph.split(",")
        if fragment.startswith("atrim=end_sample")
    )
    assert samples == timeline.total_frames * (AUDIO_SAMPLE_RATE // 30)


def test_the_mux_copies_both_streams_and_never_shortens_either() -> None:
    plan = _plan(7.3)
    mux = _command(plan, _OUTPUT)
    assert mux[-1] == _OUTPUT
    assert "-shortest" not in mux
    assert mux[mux.index("-c:v") + 1] == "copy"
    assert mux[mux.index("-c:a") + 1] == "copy"
    assert "+faststart" in mux


def test_the_mux_is_the_last_command_and_the_parts_come_first() -> None:
    plan = _plan(7.3, 9.0)
    assert plan.commands[-1][-1] == _OUTPUT
    assert f"{_WORKDIR}/scene-parts/hold-1.mp4" in plan.commands[0]


def test_an_fps_the_sample_rate_cannot_divide_is_refused() -> None:
    with pytest.raises(ValueError, match="sample rate"):
        _plan(7.3, fps=7)


def test_a_part_for_every_scene_is_required() -> None:
    timeline = _timeline(7.3, 9.0)
    with pytest.raises(ValueError, match="one part per scene"):
        plan_assembly(_parts(1), timeline, workdir=_WORKDIR, output_path=_OUTPUT)
