"""The ffmpeg commands that turn rendered scene parts into one video.

Two rules keep the slides on their narration. Video is measured in frames, so
a held slide is ``-frames:v N`` and never a duration ffmpeg has to round.
Audio is measured in samples, so each narration is padded with silence and cut
at ``frames * samples_per_frame``: the track then lands on every scene boundary
exactly, and the file is as long as the sum of the scene holds to the sample.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import PurePosixPath
from typing import Final

from .scene_timeline import SceneHold, Timeline

# Chosen because it divides by every frame rate the harness accepts, so one
# frame is always a whole number of samples.
AUDIO_SAMPLE_RATE: Final = 48_000
_AUDIO_BITRATE: Final = "128k"
# The still hold is one image repeated; the encoder is told so, and matched to
# Remotion's output so the parts concatenate without being re-encoded.
_STILL_PRESET: Final = "veryfast"
_STILL_CRF: Final = "18"
# Matched to what Remotion writes for the entrances. A held slide encoded in a
# different pixel format, colour range or time scale concatenates without an
# error and plays at the wrong speed, so these three are not cosmetic.
_PIXEL_FORMAT: Final = "yuvj420p"
_COLOR_RANGE: Final = "pc"
_TIME_SCALE: Final = "90000"


@dataclass(frozen=True, slots=True)
class ScenePart:
    """What Remotion rendered for one scene."""

    slide_number: int
    entrance_video: str
    still_image: str
    narration_audio: str


@dataclass(frozen=True, slots=True)
class AssemblyPlan:
    """Everything ffmpeg needs, in the order it must happen."""

    concat_manifest_path: str
    concat_manifest: str
    commands: tuple[tuple[str, ...], ...]


def samples_per_frame(fps: int) -> int:
    """Audio samples in one frame, or a refusal if it is not a whole number."""
    if fps <= 0 or AUDIO_SAMPLE_RATE % fps:
        raise ValueError(
            f"{fps} fps does not divide the {AUDIO_SAMPLE_RATE} Hz sample rate"
        )
    return AUDIO_SAMPLE_RATE // fps


def plan_assembly(
    parts: Sequence[ScenePart],
    timeline: Timeline,
    *,
    workdir: PurePosixPath,
    output_path: str,
) -> AssemblyPlan:
    """Plan the assembly of one video from its rendered scene parts."""
    if len(parts) != len(timeline.scenes):
        raise ValueError("assembly needs one part per scene")
    for part, scene in zip(parts, timeline.scenes, strict=True):
        if part.slide_number != scene.slide_number:
            raise ValueError("scene parts must be in the timeline's order")

    per_frame = samples_per_frame(timeline.fps)
    stage = PurePosixPath(str(workdir)) / "scene-parts"
    holds = [_hold_path(stage, scene) for scene in timeline.scenes]
    video_path = str(stage / "video.mp4")
    narration_path = str(stage / "narration.m4a")
    manifest_path = str(stage / "video-parts.txt")

    commands = [
        _still_hold_command(part, scene, hold)
        for part, scene, hold in zip(parts, timeline.scenes, holds, strict=True)
        if hold is not None
    ]
    commands.append(_concat_command(manifest_path, video_path))
    commands.append(_narration_command(parts, timeline, narration_path, per_frame))
    commands.append(_mux_command(video_path, narration_path, output_path))

    return AssemblyPlan(
        concat_manifest_path=manifest_path,
        concat_manifest=_manifest(parts, holds),
        commands=tuple(commands),
    )


def _hold_path(stage: PurePosixPath, scene: SceneHold) -> str | None:
    """Where a scene's held slide goes, or nothing when it is all entrance."""
    if scene.still_frames <= 0:
        return None
    return str(stage / f"hold-{scene.slide_number}.mp4")


def _manifest(parts: Sequence[ScenePart], holds: Sequence[str | None]) -> str:
    lines = []
    for part, hold in zip(parts, holds, strict=True):
        lines.append(f"file '{part.entrance_video}'")
        if hold is not None:
            lines.append(f"file '{hold}'")
    return "\n".join(lines)


def _still_hold_command(
    part: ScenePart, scene: SceneHold, hold: str
) -> tuple[str, ...]:
    return (
        "ffmpeg",
        "-y",
        "-v",
        "error",
        "-loop",
        "1",
        "-framerate",
        str(scene.fps),
        "-i",
        part.still_image,
        "-frames:v",
        str(scene.still_frames),
        "-c:v",
        "libx264",
        "-preset",
        _STILL_PRESET,
        "-tune",
        "stillimage",
        "-crf",
        _STILL_CRF,
        "-pix_fmt",
        _PIXEL_FORMAT,
        "-color_range",
        _COLOR_RANGE,
        "-video_track_timescale",
        _TIME_SCALE,
        "-an",
        hold,
    )


def _concat_command(manifest_path: str, video_path: str) -> tuple[str, ...]:
    return (
        "ffmpeg",
        "-y",
        "-v",
        "error",
        "-f",
        "concat",
        "-safe",
        "0",
        "-i",
        manifest_path,
        "-c",
        "copy",
        "-an",
        video_path,
    )


def _narration_command(
    parts: Sequence[ScenePart],
    timeline: Timeline,
    narration_path: str,
    per_frame: int,
) -> tuple[str, ...]:
    inputs: list[str] = []
    chains: list[str] = []
    for index, (part, scene) in enumerate(zip(parts, timeline.scenes, strict=True)):
        inputs += ["-i", part.narration_audio]
        chains.append(
            f"[{index}:a]"
            f"aformat=sample_fmts=fltp:sample_rates={AUDIO_SAMPLE_RATE}"
            ":channel_layouts=mono,"
            "apad,"
            f"atrim=end_sample={scene.frames * per_frame},"
            "asetpts=N/SR/TB"
            f"[a{index}]"
        )
    labels = "".join(f"[a{index}]" for index in range(len(parts)))
    filtergraph = (
        ";".join(chains) + f";{labels}concat=n={len(parts)}:v=0:a=1[narration]"
    )
    return (
        "ffmpeg",
        "-y",
        "-v",
        "error",
        *inputs,
        "-filter_complex",
        filtergraph,
        "-map",
        "[narration]",
        "-c:a",
        "aac",
        "-b:a",
        _AUDIO_BITRATE,
        "-ar",
        str(AUDIO_SAMPLE_RATE),
        "-ac",
        "1",
        narration_path,
    )


def _mux_command(
    video_path: str, narration_path: str, output_path: str
) -> tuple[str, ...]:
    return (
        "ffmpeg",
        "-y",
        "-v",
        "error",
        "-i",
        video_path,
        "-i",
        narration_path,
        "-c:v",
        "copy",
        "-c:a",
        "copy",
        "-movflags",
        "+faststart",
        output_path,
    )
