"""Videos finished before durations were stored get one, measured from the file."""

from __future__ import annotations

import shutil
import subprocess

import pytest
from fastapi import Response

from app.artifacts.media.backfill_durations import backfill_durations
from app.notebooks.routes import studio

pytestmark = [
    pytest.mark.integration,
    pytest.mark.skipif(
        shutil.which("ffmpeg") is None, reason="ffmpeg is not installed"
    ),
]


def _mp4(tmp_path, seconds: int) -> bytes:
    path = tmp_path / f"clip-{seconds}.mp4"
    subprocess.run(
        [
            "ffmpeg",
            "-v",
            "error",
            "-f",
            "lavfi",
            "-i",
            f"testsrc=size=160x90:rate=10:duration={seconds}",
            "-f",
            "lavfi",
            "-i",
            f"sine=duration={seconds}",
            "-shortest",
            "-pix_fmt",
            "yuv420p",
            str(path),
        ],
        check=True,
    )
    return path.read_bytes()


async def test_backfill_measures_old_videos_and_leaves_the_rest(
    db_session, make_owner, make_notebook, make_studio_output, tmp_path
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    old = await make_studio_output(
        notebook,
        kind="video",
        artifact_format="video",
        files=(("old.mp4", "video/mp4", _mp4(tmp_path, 3)),),
        metadata={"verification": {"verified": True}},
    )
    recorded = await make_studio_output(
        notebook,
        kind="video",
        artifact_format="video",
        files=(("new.mp4", "video/mp4", b"not read"),),
        metadata={"duration_seconds": 61.0},
    )
    broken = await make_studio_output(
        notebook,
        kind="video",
        artifact_format="video",
        files=(("broken.mp4", "video/mp4", b"garbage"),),
    )

    report = await backfill_durations(db_session)

    assert report.videos == [old.artifact.id]
    assert report.skipped == [broken.artifact.id]
    await db_session.refresh(old.artifact)
    assert abs(old.artifact.artifact_metadata["duration_seconds"] - 3.0) < 0.3
    assert old.artifact.artifact_metadata["verification"] == {"verified": True}
    await db_session.refresh(recorded.artifact)
    assert recorded.artifact.artifact_metadata == {"duration_seconds": 61.0}

    items = await studio.get_outputs(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )
    assert sorted(item.duration_seconds or 0 for item in items) == [0, 3, 61]

    again = await backfill_durations(db_session)
    assert again.videos == [] and again.skipped == [broken.artifact.id]
