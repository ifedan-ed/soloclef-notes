"""Studio outputs carry their workspace's System Instructions, and only those."""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from pydantic import TypeAdapter
from sqlalchemy import select

from app.db import DeliverableJob
from app.deliverables.artifact.request import ArtifactJobRequestV1
from app.deliverables.video import VideoJobRequestV1
from app.notebooks.routes import studio
from app.notebooks.studio import start
from app.notebooks.studio.options import StudioRequest

pytestmark = pytest.mark.integration

_parse = TypeAdapter(StudioRequest).validate_python

GUIDANCE = "Write for parents: plain words, no jargon."


@pytest.fixture(autouse=True)
def _sandbox_on(monkeypatch):
    monkeypatch.setattr(studio, "is_sandbox_enabled", lambda: True)


def _dispatchers():
    calls = SimpleNamespace(jobs=[])
    return start.Dispatchers(
        deliverable=lambda *, job_id, task_id: calls.jobs.append(job_id),
        podcast_draft=lambda podcast_id, ws: None,
    )


async def _generate(db_session, owner, notebook, payload):
    await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse(payload),
        db_session,
        owner.auth,
        _dispatchers(),
    )
    return await db_session.scalar(
        select(DeliverableJob)
        .where(DeliverableJob.workspace_id == owner.workspace.id)
        .order_by(DeliverableJob.id.desc())
    )


async def _owner_with_notebook(make_owner, make_document, make_notebook):
    owner = await make_owner()
    doc = await make_document(owner.workspace.id, title="Src", text="Source text.")
    notebook = await make_notebook(owner.workspace.id, sources=[("document", doc.id)])
    return owner, notebook


async def test_artifact_and_video_jobs_carry_the_workspace_guidance(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook = await _owner_with_notebook(
        make_owner, make_document, make_notebook
    )
    owner.workspace.qna_custom_instructions = GUIDANCE
    await db_session.flush()

    report = await _generate(
        db_session, owner, notebook, {"kind": "report", "request_id": "r1"}
    )
    prompt = report.request["prompt"]
    assert GUIDANCE in prompt
    assert prompt.index("</workspace_instructions>") < prompt.index("<source_material>")
    ArtifactJobRequestV1.model_validate(report.request)

    video = await _generate(
        db_session, owner, notebook, {"kind": "video", "format": "short"}
    )
    assert GUIDANCE in video.request["brief"]
    VideoJobRequestV1.model_validate(video.request)


async def test_another_workspaces_guidance_never_reaches_this_one(
    db_session, make_owner, make_document, make_notebook
):
    guided, _ = await _owner_with_notebook(make_owner, make_document, make_notebook)
    guided.workspace.qna_custom_instructions = GUIDANCE
    plain, notebook = await _owner_with_notebook(
        make_owner, make_document, make_notebook
    )
    await db_session.flush()

    job = await _generate(
        db_session, plain, notebook, {"kind": "quiz", "request_id": "q1"}
    )

    assert GUIDANCE not in job.request["prompt"]
    assert "workspace_instructions" not in job.request["prompt"]


async def test_the_longest_guidance_still_fits_the_video_brief(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook = await _owner_with_notebook(
        make_owner, make_document, make_notebook
    )
    owner.workspace.qna_custom_instructions = "<" * 4_000
    await db_session.flush()

    video = await _generate(
        db_session,
        owner,
        notebook,
        {
            "kind": "video",
            "format": "explainer",
            "focus": "f" * 2_000,
            "visual_style": "custom",
            "style_prompt": "s" * 300,
        },
    )

    VideoJobRequestV1.model_validate(video.request)
