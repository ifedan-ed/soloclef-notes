"""An interactive report embeds the notebook's newest finished Studio outputs."""

from __future__ import annotations

import io
import json
from types import SimpleNamespace

import pytest
from pydantic import TypeAdapter
from sqlalchemy import select

from app.db import DeliverableJob, DeliverableJobStatus
from app.deliverables.artifact.required_fragment import (
    RequiredFragment,
    ensure_fragment,
)
from app.notebooks.routes import studio
from app.notebooks.studio import start
from app.notebooks.studio.options import StudioRequest
from app.notebooks.studio.report_embeds.collect import collect_embeds
from app.notebooks.studio.report_embeds.render import EMBED_MARKER

pytestmark = pytest.mark.integration

_parse = TypeAdapter(StudioRequest).validate_python


def _deck(prefix: str) -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "title": "Deck",
            "cards": [
                {"front_text": f"{prefix} {n}", "back_text": f"Answer {n}"}
                for n in range(1, 16)
            ],
        }
    ).encode()


def _quiz() -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "title": "Quiz",
            "questions": [
                {
                    "question_text": f"Question number {n}?",
                    "options": ["Alpha", "Beta", "Gamma", "Delta"],
                    "correct_option_index": n % 4,
                    "explanation_text": f"Because {n}.",
                }
                for n in range(1, 6)
            ],
        }
    ).encode()


def _xlsx() -> bytes:
    from openpyxl import Workbook

    book = Workbook()
    sheet = book.active
    sheet.append(["Marker", "Value"])
    sheet.append(["Ferritin", 30])
    sheet.append([None, None])
    sheet.append(["Iron", 12.5])
    buffer = io.BytesIO()
    book.save(buffer)
    return buffer.getvalue()


XLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"


async def _stocked_notebook(make_owner, make_notebook, make_studio_output):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    add = make_studio_output
    await add(
        notebook,
        kind="flashcards",
        artifact_format="flashcards",
        files=(("deck.json", "application/json", _deck("Old")),),
    )
    await add(
        notebook,
        kind="flashcards",
        artifact_format="flashcards",
        files=(("deck.json", "application/json", _deck("New")),),
    )
    await add(
        notebook,
        kind="quiz",
        artifact_format="quiz",
        files=(("quiz.json", "application/json", _quiz()),),
    )
    await add(
        notebook,
        kind="data_table",
        artifact_format="xlsx",
        files=(("table.xlsx", XLSX, _xlsx()),),
    )
    video = await add(
        notebook,
        kind="video",
        artifact_format="video",
        files=(("film.mp4", "video/mp4", b"\x00" * 64),),
        metadata={"duration_seconds": 487.2},
    )
    # A failed generation and an unreadable one are left out, not fatal.
    await add(
        notebook,
        kind="audio",
        artifact_format="podcast",
        status=DeliverableJobStatus.FAILED,
    )
    await add(
        notebook,
        kind="mindmap",
        artifact_format="mindmap",
        files=(("map.png", "image/png", b"png"),),
        title="Map",
    )
    return owner, notebook, video


async def test_collect_takes_the_newest_ready_output_of_each_kind(
    db_session, make_owner, make_notebook, make_studio_output
):
    _owner, notebook, video = await _stocked_notebook(
        make_owner, make_notebook, make_studio_output
    )

    embeds = await collect_embeds(db_session, notebook)

    assert sorted(embeds.kinds) == ["data_table", "flashcards", "quiz", "video"]
    assert embeds.flashcards.payload[0][0] == "New 1"
    assert len(embeds.quiz.payload) == 5 and embeds.quiz.payload[0][2] == 1
    assert embeds.data_table.payload == [
        ["Marker", "Value"],
        ["Ferritin", "30"],
        ["Iron", "12.5"],
    ]
    assert (embeds.video.artifact_id, embeds.video.file_id) == (
        video.artifact.id,
        video.files[0].id,
    )
    assert embeds.video.duration_seconds == 487
    # The mind map's placeholder document has no outline, so it is skipped.
    assert embeds.mindmap is None and embeds.audio is None


async def test_interactive_report_job_carries_the_embeds_section(
    db_session,
    make_owner,
    make_document,
    make_notebook,
    make_studio_output,
    monkeypatch,
):
    owner, notebook, video = await _stocked_notebook(
        make_owner, make_notebook, make_studio_output
    )
    document = await make_document(owner.workspace.id, text="Report source text.")
    from app.notebooks.persistence import NotebookSource

    db_session.add(NotebookSource(notebook_id=notebook.id, document_id=document.id))
    await db_session.flush()
    monkey = SimpleNamespace(jobs=[])
    dispatchers = start.Dispatchers(
        deliverable=lambda *, job_id, task_id: monkey.jobs.append(job_id)
    )
    monkeypatch.setattr(studio, "is_sandbox_enabled", lambda: True)

    await studio.generate(
        owner.workspace.id,
        notebook.id,
        _parse({"kind": "report", "format": "interactive", "request_id": "r1"}),
        db_session,
        owner.auth,
        dispatchers,
    )

    job = (
        await db_session.scalars(
            select(DeliverableJob).where(DeliverableJob.tool_call_id.like("%:r1"))
        )
    ).one()
    fragment = job.request["required_fragment"]
    assert fragment["path"] == "/workspace/studio-embeds.html"
    assert fragment["marker"] == EMBED_MARKER
    assert (
        f'data-surfsense-media="{video.artifact.id}:{video.files[0].id}"'
        in (fragment["html"])
    )
    assert "/workspace/studio-embeds.html" in job.request["prompt"]
    assert "video overview" in job.request["prompt"]


async def test_document_reports_and_empty_notebooks_embed_nothing(
    db_session, make_owner, make_document, make_notebook, monkeypatch
):
    owner = await make_owner()
    document = await make_document(owner.workspace.id)
    notebook = await make_notebook(
        owner.workspace.id, sources=[("document", document.id)]
    )
    dispatchers = start.Dispatchers(deliverable=lambda **_: None)
    monkeypatch.setattr(studio, "is_sandbox_enabled", lambda: True)
    for request_id, fmt in (("d1", "document"), ("i1", "interactive")):
        await studio.generate(
            owner.workspace.id,
            notebook.id,
            _parse({"kind": "report", "format": fmt, "request_id": request_id}),
            db_session,
            owner.auth,
            dispatchers,
        )
    for job in (await db_session.scalars(select(DeliverableJob))).all():
        if job.workspace_id == owner.workspace.id:
            assert job.request.get("required_fragment") is None
            assert "studio-embeds" not in job.request["prompt"]


@pytest.fixture
def saving(monkeypatch, artifact_store, patched_embed_texts):
    """save_artifact against the throwaway store, on the indexed (non-git) path."""
    from unittest.mock import AsyncMock

    monkeypatch.setattr(
        "app.artifacts.service.get_storage_backend", lambda *a, **k: artifact_store
    )
    monkeypatch.setattr(
        "app.artifacts.service.knowledge_store_enabled_for",
        AsyncMock(return_value=False),
    )


async def _html_report(db_session, make_owner, make_notebook, make_studio_output, html):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    made = await make_studio_output(
        notebook,
        kind="report",
        artifact_format="html",
        files=(("report.html", "text/html", html.encode()),),
    )
    made.job.thread_id = None
    return made


_FRAGMENT = RequiredFragment(
    path="/workspace/studio-embeds.html",
    html=f"<section {EMBED_MARKER}>embeds</section>",
    marker=EMBED_MARKER,
)


async def test_a_report_saved_without_the_section_gets_it_appended(
    db_session, make_owner, make_notebook, make_studio_output, artifact_store, saving
):
    made = await _html_report(
        db_session, make_owner, make_notebook, make_studio_output, "<h1>Report</h1>"
    )

    added = await ensure_fragment(db_session, made.job, made.artifact.id, _FRAGMENT)

    assert added is True
    from sqlalchemy.orm import selectinload

    from app.artifacts.persistence import Artifact

    artifact = await db_session.scalar(
        select(Artifact)
        .options(selectinload(Artifact.files))
        .where(Artifact.id == made.artifact.id)
        .execution_options(populate_existing=True)
    )
    assert artifact.generation == 2
    (primary,) = artifact.files
    stored = b"".join(
        [chunk async for chunk in artifact_store.open_stream(primary.storage_key)]
    ).decode()
    assert stored.startswith("<h1>Report</h1>")
    assert stored.rstrip().endswith(_FRAGMENT.html)


async def test_a_report_that_already_has_the_section_is_untouched(
    db_session, make_owner, make_notebook, make_studio_output, saving
):
    made = await _html_report(
        db_session,
        make_owner,
        make_notebook,
        make_studio_output,
        f"<h1>Report</h1><section {EMBED_MARKER}>embeds</section>",
    )

    assert (
        await ensure_fragment(db_session, made.job, made.artifact.id, _FRAGMENT)
        is False
    )
    await db_session.refresh(made.artifact)
    assert made.artifact.generation == 1
