"""Refuse an assembled file that does not sit on the timeline's frame grid.

Concatenating video without re-encoding is silent about mismatched streams:
the command succeeds and the file plays at the wrong speed, which reaches a
viewer as narration sliding off its slides. This reads the finished file back
and compares it with the grid it was planned on.
"""

from __future__ import annotations

import json
import shlex
from fractions import Fraction

from app.sandbox import SandboxSession

from .scene_timeline import Timeline


async def assert_on_grid(
    sandbox: SandboxSession, path: str, timeline: Timeline
) -> None:
    """Fail unless the file holds the planned frames and the matching audio."""
    probed = await _probe(sandbox, path)
    video = _stream(probed, "video")
    audio = _stream(probed, "audio")

    frames = _integer(video, "nb_frames")
    if frames != timeline.total_frames:
        raise RuntimeError(
            f"assembled video holds {frames} frames, not the planned "
            f"{timeline.total_frames}"
        )
    tolerance = Fraction(1, timeline.fps)
    for name, stream in (("video", video), ("audio", audio)):
        measured = _fraction(stream, "duration")
        if abs(measured - timeline.total_seconds) > tolerance:
            raise RuntimeError(
                f"assembled {name} runs {float(measured):.3f}s, not the planned "
                f"{float(timeline.total_seconds):.3f}s"
            )


def _integer(stream: dict, field: str) -> int:
    try:
        return int(stream[field])
    except (KeyError, TypeError, ValueError) as error:
        raise RuntimeError(f"the assembled video reports no {field}") from error


def _fraction(stream: dict, field: str) -> Fraction:
    try:
        return Fraction(str(stream[field]))
    except (KeyError, TypeError, ValueError) as error:
        raise RuntimeError(f"the assembled video reports no {field}") from error


async def _probe(sandbox: SandboxSession, path: str) -> dict:
    result = await sandbox.run_command(
        "ffprobe -v error -show_entries stream=codec_type,nb_frames,duration "
        "-of json -- " + shlex.quote(path)
    )
    if not result.ok:
        raise RuntimeError(f"Could not probe the assembled video: {result.output}")
    try:
        return json.loads(result.output)
    except json.JSONDecodeError as error:
        raise RuntimeError("Could not read the assembled video's streams") from error


def _stream(probed: dict, codec_type: str) -> dict:
    for stream in probed.get("streams", []):
        if stream.get("codec_type") == codec_type:
            return stream
    raise RuntimeError(f"the assembled video has no {codec_type} stream")
