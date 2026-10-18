"""Studio generation starts the right job with the chosen sources, and only those."""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from fastapi import HTTPException, Response
from pydantic import TypeAdapter
from sqlalchemy import select

from app.db import DeliverableJob, DeliverableJobStatus, NewChatThread
from app.deliverables.jobs.policy import ARTIFACT_KIND, VIDEO_KIND
from app.deliverables.video import VideoJobRequestV1
from app.notebooks.persistence import NotebookOutput
from app.notebooks.routes import studio
from app.notebooks.studio import start
from app.notebooks.studio.options import StudioRequest
from app.podcasts.persistence import Podcast, PodcastStatus
from app.podcasts.schemas import (
    DurationTarget,
    PodcastSpec,
    PodcastStyle,
    SpeakerRole,
    SpeakerSpec,
)

pytestmark = pytest.mark.integration

_parse = TypeAdapter(StudioRequest).validate_python


@pytest.fixture(autouse=True)
def _sandbox_on(monkeypatch):
    monkeypatch.setattr(studio, "is_sandbox_enabled", lambda: True)


def _dispatchers():
    calls = SimpleNamespace(jobs=[], drafts=[])
    dispatchers = start.Dispatchers(
        deliverable=lambda *, job_id, task_id: calls.jobs.append((job_id, task_id)),
        podcast_draft=lambda podcast_id, ws: calls.drafts.append(podcast_id),
    )
    return dispatchers, calls


async def _setup(make_owner, make_document, make_notebook):
    """Returns the owner, the notebook and the *source row ids* of two documents."""
    owner = await make_owner()
    ws = owner.workspace.id
    chosen = await make_document(ws, title="Chosen", text="Chosen source text.")
    skipped = await make_document(ws, title="Skipped", text="Skipped source text.")
    notebook = await make_notebook(
        ws, sources=[("document", chosen.id), ("document", skipped.id)]
    )
    rows = {row.document_id: row.id for row in notebook.source_rows}
    return (
        owner,
        notebook,
        SimpleNamespace(id=rows[chosen.id], document_id=chosen.id),
        SimpleNamespace(id=rows[skipped.id], document_id=skipped.id),
    )


async def test_quiz_enqueues_an_artifact_job_with_only_the_chosen_sources(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook, chosen, _ = await _setup(make_owner, make_document, make_notebook)
    dispatchers, calls = _dispatchers()

    item = await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse(
            {
                "kind": "quiz",
                "source_ids": [chosen.id],
                "count": "more",
                "difficulty": "hard",
                "focus": "iron metabolism",
                "request_id": "abc123",
            }
        ),
        db_session,
        owner.auth,
        dispatchers,
    )

    assert item.status == "queued" and item.source_count == 1
    job = await db_session.scalar(select(DeliverableJob))
    assert job.kind == ARTIFACT_KIND
    assert job.tool_call_id == f"notebook-studio:{notebook.id}:abc123"
    assert job.request["output"] == "quiz"
    assert "Chosen source text." in job.request["prompt"]
    assert "Skipped source text." not in job.request["prompt"]
    assert "exactly 20" in job.request["prompt"] and "hard" in job.request["prompt"]
    assert job.request["item_count"] == {"target": 20, "minimum": 18}
    assert "iron metabolism" in job.request["prompt"]
    thread = await db_session.get(NewChatThread, job.thread_id)
    assert thread.notebook_id == notebook.id and thread.source == "artifact_api"
    assert job.request["root_thread_id"] == thread.id
    assert calls.jobs == [(job.id, job.celery_task_id)]

    # The same click again starts nothing new.
    again = await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse({"kind": "quiz", "source_ids": [chosen.id], "request_id": "abc123"}),
        db_session,
        owner.auth,
        dispatchers,
    )
    assert again.id == item.id
    assert len(calls.jobs) == 1


async def test_sources_from_another_workspace_are_refused(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook, _, _ = await _setup(make_owner, make_document, make_notebook)
    other = await make_owner()
    foreign_doc = await make_document(other.workspace.id, title="Foreign")
    foreign = await make_notebook(
        other.workspace.id, sources=[("document", foreign_doc.id)]
    )
    dispatchers, calls = _dispatchers()

    with pytest.raises(HTTPException) as exc:
        await studio.generate(
            owner.workspace.id,
            notebook.id,
            _parse(
                {
                    "kind": "report",
                    "source_ids": [foreign.source_rows[0].id, foreign_doc.id],
                }
            ),
            db_session,
            owner.auth,
            dispatchers,
        )
    assert exc.value.status_code == 404
    assert calls.jobs == []
    assert await db_session.scalar(select(DeliverableJob)) is None


async def test_an_outsider_cannot_generate_from_the_notebook(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook, chosen, _ = await _setup(make_owner, make_document, make_notebook)
    outsider = await make_owner()
    dispatchers, calls = _dispatchers()
    for workspace_id, expected in (
        (owner.workspace.id, 403),
        (outsider.workspace.id, 404),
    ):
        with pytest.raises(HTTPException) as exc:
            await studio.generate(
                workspace_id,
                notebook.id,
                _parse({"kind": "mindmap", "source_ids": [chosen.id]}),
                db_session,
                outsider.auth,
                dispatchers,
            )
        assert exc.value.status_code == expected
    assert calls.jobs == []


async def test_video_uses_the_queued_video_job(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook, _, _ = await _setup(make_owner, make_document, make_notebook)
    dispatchers, calls = _dispatchers()

    await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse(
            {
                "kind": "video",
                "format": "short",
                "language": "Spanish",
                "focus": "x",
                "visual_style": "whiteboard",
            }
        ),
        db_session,
        owner.auth,
        dispatchers,
    )

    job = await db_session.scalar(select(DeliverableJob))
    assert job.kind == VIDEO_KIND
    assert job.request["root_thread_id"] == job.thread_id
    assert "60 to 90 seconds" in job.request["brief"]
    assert "Spanish" in job.request["brief"]
    assert "Visual style: Whiteboard" in job.request["brief"]
    # No source ids given: the enabled sources are used.
    assert set(job.request["source_references"]) == {"Chosen", "Skipped"}
    # The source text travels whole beside the brief, sections intact.
    assert "## Source: Chosen\n\n" in job.request["source_material"]
    assert "Chosen source text." in job.request["source_material"]
    assert job.request["source_condensed"] is False
    VideoJobRequestV1.model_validate(job.request)
    assert len(calls.jobs) == 1


async def test_audio_starts_drafting_with_the_chosen_format(
    db_session, make_owner, make_document, make_notebook, monkeypatch
):
    owner, notebook, chosen, _ = await _setup(make_owner, make_document, make_notebook)
    dispatchers, calls = _dispatchers()
    seen = {}

    async def fake_spec(session, *, workspace_id, plan, language):
        seen["plan"] = plan
        return PodcastSpec(
            language="en",
            style=plan.style,
            speakers=[
                SpeakerSpec(slot=0, name="Host", role=SpeakerRole.HOST, voice_id="v1")
            ],
            duration=DurationTarget(
                min_seconds=plan.min_seconds, max_seconds=plan.max_seconds
            ),
            focus=plan.focus,
        )

    monkeypatch.setattr(start, "propose_audio_spec", fake_spec)

    item = await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse(
            {
                "kind": "audio",
                "format": "brief",
                "length": "short",
                "source_ids": [chosen.id],
            }
        ),
        db_session,
        owner.auth,
        dispatchers,
    )

    podcast = await db_session.scalar(select(Podcast))
    assert podcast.status == PodcastStatus.DRAFTING
    assert "Chosen source text." in podcast.source_content
    assert "Skipped source text." not in podcast.source_content
    assert seen["plan"].style == PodcastStyle.MONOLOGUE
    assert seen["plan"].speaker_count == 1
    assert (seen["plan"].min_seconds, seen["plan"].max_seconds) == (60, 150)
    assert calls.drafts == [podcast.id]
    assert item.kind == "audio" and item.status == "running"


async def test_outputs_list_reflects_the_job_state(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook, chosen, _ = await _setup(make_owner, make_document, make_notebook)
    dispatchers, _ = _dispatchers()
    await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse({"kind": "slides", "source_ids": [chosen.id]}),
        db_session,
        owner.auth,
        dispatchers,
    )
    job = await db_session.scalar(select(DeliverableJob))
    job.status = DeliverableJobStatus.RUNNING
    job.progress = 40
    job.phase = "generating"
    await db_session.flush()

    items = await studio.get_outputs(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )
    assert [(i.kind, i.status, i.progress) for i in items] == [
        ("slides", "running", 40)
    ]

    await studio.delete_output(
        owner.workspace.id, notebook.id, items[0].id, db_session, owner.auth
    )
    assert await db_session.scalar(select(NotebookOutput)) is None
    await db_session.refresh(job)
    assert job.status == DeliverableJobStatus.CANCELLING


async def test_a_folder_source_contributes_the_documents_inside_it(
    db_session, make_owner, make_document, make_folder, make_notebook
):
    owner = await make_owner()
    ws = owner.workspace.id
    folder = await make_folder(ws, name="Papers")
    await make_document(
        ws, title="In folder A", text="Alpha text.", folder_id=folder.id
    )
    await make_document(ws, title="In folder B", text="Beta text.", folder_id=folder.id)
    await make_document(ws, title="Loose", text="Loose text.")
    notebook = await make_notebook(ws, sources=[("folder", folder.id)])
    dispatchers, _ = _dispatchers()

    item = await studio.generate(
        ws,
        notebook.id,
        _parse({"kind": "data_table", "source_ids": [notebook.source_rows[0].id]}),
        db_session,
        owner.auth,
        dispatchers,
    )

    job = await db_session.scalar(select(DeliverableJob))
    assert "Alpha text." in job.request["prompt"]
    assert "Beta text." in job.request["prompt"]
    assert "Loose text." not in job.request["prompt"]
    assert item.source_count == 2


async def test_infographic_job_carries_the_dialog_style_so_the_run_never_asks(
    db_session, make_owner, make_document, make_notebook
):
    owner, notebook, chosen, _ = await _setup(make_owner, make_document, make_notebook)
    dispatchers, calls = _dispatchers()

    item = await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse(
            {
                "kind": "infographic",
                "source_ids": [chosen.id],
                "style": "clay",
                "orientation": "portrait",
                "detail": "concise",
                "focus": "Use a blue theme",
                "request_id": "info1",
            }
        ),
        db_session,
        owner.auth,
        dispatchers,
    )

    assert item.kind == "infographic" and item.status == "queued"
    job = await db_session.scalar(select(DeliverableJob))
    assert job.kind == ARTIFACT_KIND
    assert job.request["output"] == "infographic"
    assert job.request["infographic_style"] == "clay"
    assert job.request["infographic_orientation"] == "portrait"
    assert job.request["infographic_detail"] == "concise"
    assert job.request["infographic_description"] == "Use a blue theme"
    assert "tall portrait page" in job.request["prompt"]
    assert "Chosen source text." in job.request["prompt"]
    assert calls.jobs == [(job.id, job.celery_task_id)]


async def test_infographic_style_outside_the_catalogue_is_refused():
    with pytest.raises(ValueError):
        _parse({"kind": "infographic", "style": "photoreal"})


async def test_studio_list_shows_a_videos_recorded_duration(
    db_session, make_owner, make_notebook, make_studio_output
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    await make_studio_output(
        notebook,
        kind="video",
        artifact_format="video",
        metadata={"verification": {"verified": True}, "duration_seconds": 487.3},
    )
    await make_studio_output(
        notebook, kind="quiz", artifact_format="quiz", metadata={"quiz": {}}
    )
    await make_studio_output(
        notebook, kind="video", artifact_format="video", metadata={"verification": {}}
    )

    items = await studio.get_outputs(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )

    seen = sorted(
        (item.kind, item.duration_seconds or 0, item.artifact_format) for item in items
    )
    assert seen == [("quiz", 0, "quiz"), ("video", 0, "video"), ("video", 487, "video")]
