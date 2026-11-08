"""A media file's duration is read from the file itself, and never breaks a save."""

from __future__ import annotations

import shutil
import subprocess

import pytest

from app.artifacts.media import duration
from app.sandbox import ExecResult
from tests.utils.fake_sandbox import FakeSandboxSession

pytestmark = pytest.mark.unit

needs_ffmpeg = pytest.mark.skipif(
    shutil.which("ffmpeg") is None or shutil.which("ffprobe") is None,
    reason="ffmpeg is not installed",
)


@pytest.mark.parametrize(
    ("output", "expected"),
    [("487.346000\n", 487.3), ("N/A\n", None), ("", None), ("0.000\n", None)],
)
def test_probe_output_is_parsed_or_ignored(output, expected):
    assert duration.parse_probe_duration(output) == expected


@needs_ffmpeg
async def test_a_real_mp3_is_measured(tmp_path):
    path = tmp_path / "tone.mp3"
    subprocess.run(
        [
            "ffmpeg",
            "-v",
            "error",
            "-f",
            "lavfi",
            "-i",
            "sine=frequency=440:duration=3",
            "-c:a",
            "libmp3lame",
            str(path),
        ],
        check=True,
    )
    measured = await duration.probe_bytes_duration(path.read_bytes(), suffix=".mp3")
    assert measured is not None and abs(measured - 3.0) < 0.2

    async def chunks():
        data = path.read_bytes()
        yield data[:1000]
        yield data[1000:]

    streamed = await duration.probe_stream_duration(chunks(), suffix=".mp3")
    assert streamed == measured


async def test_garbage_is_not_a_duration():
    assert await duration.probe_bytes_duration(b"not media", suffix=".mp4") is None


async def test_sandbox_probe_reads_ffprobe_output():
    sandbox = FakeSandboxSession(
        command_handler=lambda command: ExecResult("62.43\n", 0)
    )
    assert await duration.probe_sandbox_duration(sandbox, "/workspace/v.mp4") == 62.4
    assert (
        "ffprobe" in sandbox.commands[0] and "/workspace/v.mp4" in sandbox.commands[0]
    )


async def test_sandbox_probe_failure_is_none():
    sandbox = FakeSandboxSession(command_handler=lambda command: ExecResult("", 1))
    assert await duration.probe_sandbox_duration(sandbox, "/workspace/v.mp4") is None
