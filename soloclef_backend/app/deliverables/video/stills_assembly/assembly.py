"""Assemble one video from per-scene stills instead of every frame.

Remotion renders each scene's entrance and the one still that carries the rest
of it; ffmpeg holds that still for the frames the narration needs and joins the
pieces. The frame grid comes from ``scene_timeline``, so the slides and their
voices stay on the same clock no matter what the renderer reports back.
"""

from __future__ import annotations

import json
import shlex
from collections.abc import Sequence
from pathlib import PurePosixPath
from typing import TYPE_CHECKING

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools.sandbox import (
    _run_bash,
)
from app.sandbox import SandboxSession

from .entrance_window import entrance_frames
from .ffmpeg_plan import ScenePart, plan_assembly
from .grid_check import assert_on_grid
from .scene_timeline import Timeline, build_timeline, with_rendered_entrances

if TYPE_CHECKING:
    from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools.synthesize_narration import (
        NarrationAudio,
    )

_STAGE_DIR = "scene-parts"
_ENTRANCE_REQUEST = "scene-parts-entrance.json"


async def assemble_video_from_stills(
    sandbox: SandboxSession,
    *,
    workdir: PurePosixPath,
    props_path: str,
    narration: Sequence[NarrationAudio],
    fps: int,
    min_frames: int,
    output_path: str,
) -> Timeline:
    """Render the scene parts and join them into ``output_path``."""
    timeline = build_timeline(
        narration,
        fps=fps,
        min_frames=min_frames,
        entrance_frames=entrance_frames(fps),
    )
    stage = workdir / _STAGE_DIR
    request_path = workdir / _ENTRANCE_REQUEST
    await sandbox.write_file(
        str(request_path),
        json.dumps([scene.entrance_frames for scene in timeline.scenes]).encode(),
    )

    result = await _run_bash(
        sandbox,
        f"cd -- {shlex.quote(str(workdir))} && "
        f"node render.mjs --scene-parts {shlex.quote(props_path)} "
        f"{shlex.quote(str(stage))} {shlex.quote(str(request_path))}",
    )
    if not result.ok:
        raise RuntimeError(
            f"Could not render the video's scene parts: {result.output[-16_000:]}"
        )

    manifest = await _read_manifest(sandbox, stage)
    timeline = with_rendered_entrances(timeline, manifest)
    plan = plan_assembly(
        _parts(manifest, timeline, workdir=workdir, narration=narration),
        timeline,
        workdir=workdir,
        output_path=output_path,
    )
    await sandbox.write_file(
        plan.concat_manifest_path, f"{plan.concat_manifest}\n".encode()
    )
    joined = " && ".join(
        " ".join(shlex.quote(argument) for argument in command)
        for command in plan.commands
    )
    result = await sandbox.run_command(joined)
    if not result.ok:
        raise RuntimeError(
            f"Could not assemble the video from its scene parts: "
            f"{result.output[-16_000:]}"
        )
    await assert_on_grid(sandbox, output_path, timeline)
    return timeline


async def _read_manifest(
    sandbox: SandboxSession, stage: PurePosixPath
) -> dict[int, dict]:
    """What Remotion says it rendered, keyed by slide."""
    raw = await sandbox.read_file(str(stage / "parts.json"))
    try:
        parts = json.loads(raw)["parts"]
    except (KeyError, TypeError, ValueError) as error:
        raise RuntimeError(
            "Remotion wrote an unreadable scene-part manifest"
        ) from error
    return {part["slide_number"]: part for part in parts}


def _parts(
    manifest: dict[int, dict],
    timeline: Timeline,
    *,
    workdir: PurePosixPath,
    narration: Sequence[NarrationAudio],
) -> list[ScenePart]:
    audio_by_slide = {item["slide_number"]: item["audio"] for item in narration}
    parts = []
    for scene in timeline.scenes:
        rendered = manifest[scene.slide_number]
        parts.append(
            ScenePart(
                slide_number=scene.slide_number,
                entrance_video=rendered["entrance_video"],
                still_image=rendered["still_image"],
                narration_audio=str(
                    workdir / "public" / audio_by_slide[scene.slide_number]
                ),
            )
        )
    return parts
