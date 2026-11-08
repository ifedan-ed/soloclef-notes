"""Assembling a video from scene parts, as the sandbox sees it."""

from __future__ import annotations

import json
from pathlib import PurePosixPath

import pytest

from app.deliverables.video.stills_assembly import assembly
from app.sandbox import ExecResult
from tests.utils.fake_sandbox import FakeSandboxSession

pytestmark = pytest.mark.unit

_WORKDIR = PurePosixPath("/workspace/job-7")
_PROPS = f"{_WORKDIR}/props.json"
_OUTPUT = "/workspace/job-7.mp4"
_NARRATION = [
    {"slide_number": 1, "audio": "slide-1.mp3", "duration_seconds": 22.248},
    {"slide_number": 2, "audio": "slide-2.mp3", "duration_seconds": 1.0},
]
_FRAMES = [668, 30]


def _manifest(entrances: list[int]) -> bytes:
    return json.dumps(
        {
            "fps": 30,
            "parts": [
                {
                    "slide_number": index,
                    "scene_frames": _FRAMES[index - 1],
                    "entrance_frames": entrance,
                    "entrance_video": f"{_WORKDIR}/scene-parts/entrance-{index}.mp4",
                    "still_image": f"{_WORKDIR}/scene-parts/still-{index}.png",
                    "still_frame": 120,
                }
                for index, entrance in enumerate(entrances, start=1)
            ],
        }
    ).encode()


def _probe_output(frames: int, seconds: float) -> str:
    return json.dumps(
        {
            "streams": [
                {
                    "codec_type": "video",
                    "nb_frames": str(frames),
                    "duration": f"{seconds:.6f}",
                },
                {"codec_type": "audio", "duration": f"{seconds:.6f}"},
            ]
        }
    )


class _Sandbox(FakeSandboxSession):
    """A sandbox that answers the manifest read and the closing probe."""

    def __init__(self, entrances: list[int], probe: str) -> None:
        super().__init__({f"{_WORKDIR}/scene-parts/parts.json": _manifest(entrances)})
        self._probe = probe

    async def run_command(self, command: str) -> ExecResult:
        self.commands.append(command)
        if command.startswith("ffprobe"):
            return ExecResult(self._probe, 0)
        return ExecResult("", 0)


async def _assemble(sandbox, monkeypatch):
    async def run_bash(session, command):
        return await session.run_command(command)

    monkeypatch.setattr(assembly, "_run_bash", run_bash)
    return await assembly.assemble_video_from_stills(
        sandbox,
        workdir=_WORKDIR,
        props_path=_PROPS,
        narration=_NARRATION,
        fps=30,
        min_frames=30,
        output_path=_OUTPUT,
    )


@pytest.mark.asyncio
async def test_it_asks_remotion_for_the_entrances_the_timeline_planned(
    monkeypatch,
) -> None:
    sandbox = _Sandbox([120, 30], _probe_output(698, 698 / 30))
    await _assemble(sandbox, monkeypatch)
    request = sandbox.writes[f"{_WORKDIR}/{assembly._ENTRANCE_REQUEST}"]
    assert json.loads(request) == [120, 30]
    assert any("render.mjs --scene-parts" in command for command in sandbox.commands)


@pytest.mark.asyncio
async def test_the_scene_part_render_is_not_the_still_preview(monkeypatch) -> None:
    """``--stills`` names the review previews, which skip the render gate."""
    sandbox = _Sandbox([120, 30], _probe_output(698, 698 / 30))
    await _assemble(sandbox, monkeypatch)
    node = next(c for c in sandbox.commands if "render.mjs" in c)
    assert "--stills" not in node


@pytest.mark.asyncio
async def test_a_shorter_rendered_entrance_lengthens_the_still_not_the_file(
    monkeypatch,
) -> None:
    sandbox = _Sandbox([119, 30], _probe_output(698, 698 / 30))
    timeline = await _assemble(sandbox, monkeypatch)
    assert timeline.total_frames == 698
    assert timeline.scenes[0].entrance_frames == 119
    assert timeline.scenes[0].still_frames == 668 - 119


@pytest.mark.asyncio
async def test_an_entrance_longer_than_planned_is_refused(monkeypatch) -> None:
    sandbox = _Sandbox([121, 30], _probe_output(698, 698 / 30))
    with pytest.raises(ValueError, match="entrance frames"):
        await _assemble(sandbox, monkeypatch)


@pytest.mark.asyncio
async def test_the_concat_manifest_is_written_before_ffmpeg_reads_it(
    monkeypatch,
) -> None:
    sandbox = _Sandbox([120, 30], _probe_output(698, 698 / 30))
    await _assemble(sandbox, monkeypatch)
    manifest = sandbox.writes[f"{_WORKDIR}/scene-parts/video-parts.txt"].decode()
    assert manifest.splitlines() == [
        f"file '{_WORKDIR}/scene-parts/entrance-1.mp4'",
        f"file '{_WORKDIR}/scene-parts/hold-1.mp4'",
        f"file '{_WORKDIR}/scene-parts/entrance-2.mp4'",
    ]


@pytest.mark.asyncio
async def test_every_ffmpeg_step_runs_in_order_in_one_command(monkeypatch) -> None:
    sandbox = _Sandbox([120, 30], _probe_output(698, 698 / 30))
    await _assemble(sandbox, monkeypatch)
    joined = next(c for c in sandbox.commands if c.startswith("ffmpeg"))
    assert joined.count("ffmpeg") == 4
    assert joined.index("hold-1.mp4") < joined.index("video-parts.txt")
    assert joined.index("narration.m4a") < joined.rindex(_OUTPUT)


@pytest.mark.asyncio
async def test_the_narration_is_read_from_the_sandbox_public_directory(
    monkeypatch,
) -> None:
    sandbox = _Sandbox([120, 30], _probe_output(698, 698 / 30))
    await _assemble(sandbox, monkeypatch)
    joined = next(c for c in sandbox.commands if c.startswith("ffmpeg"))
    assert f"{_WORKDIR}/public/slide-1.mp3" in joined
    assert f"{_WORKDIR}/public/slide-2.mp3" in joined


@pytest.mark.asyncio
async def test_a_file_that_came_out_the_wrong_length_is_refused(
    monkeypatch,
) -> None:
    sandbox = _Sandbox([120, 30], _probe_output(698, 113.1))
    with pytest.raises(RuntimeError, match="not the planned"):
        await _assemble(sandbox, monkeypatch)


@pytest.mark.asyncio
async def test_a_file_missing_frames_is_refused(monkeypatch) -> None:
    sandbox = _Sandbox([120, 30], _probe_output(690, 698 / 30))
    with pytest.raises(RuntimeError, match="frames"):
        await _assemble(sandbox, monkeypatch)


@pytest.mark.asyncio
async def test_a_failed_scene_part_render_says_so(monkeypatch) -> None:
    sandbox = _Sandbox([120, 30], _probe_output(698, 698 / 30))

    async def failing(session, command):
        return ExecResult("scene 2 threw", 1)

    monkeypatch.setattr(assembly, "_run_bash", failing)
    with pytest.raises(RuntimeError, match="scene parts"):
        await assembly.assemble_video_from_stills(
            sandbox,
            workdir=_WORKDIR,
            props_path=_PROPS,
            narration=_NARRATION,
            fps=30,
            min_frames=30,
            output_path=_OUTPUT,
        )
