"""The report reading view on real Postgres: suggestions cached with the report
version, Add starting a normal Studio generation, dismiss, isolation."""

from __future__ import annotations

from types import SimpleNamespace

import pytest
from fastapi import HTTPException, Response
from sqlalchemy import select

from app.artifacts.persistence import Artifact
from app.db import DeliverableJob, Document
from app.notebooks.persistence import NotebookOutput
from app.notebooks.routes import report_view as routes, studio
from app.notebooks.studio import start
from app.notebooks.studio.report_view import service
from app.notebooks.studio.report_view.schemas import ReportSuggestion

pytestmark = pytest.mark.integration

REPORT_MD = """# PICU Orientation

Welcome.

## Rounds

Day rounds start at 08:00.

## Escalation

Call the fellow first.

## Summary

Be early.
"""


@pytest.fixture(autouse=True)
def _sandbox_on(monkeypatch):
    monkeypatch.setattr(routes, "is_sandbox_enabled", lambda: True)
    monkeypatch.setattr(studio, "is_sandbox_enabled", lambda: True)


def _dispatchers():
    calls = SimpleNamespace(jobs=[], drafts=[])
    return (
        start.Dispatchers(
            deliverable=lambda *, job_id, task_id: calls.jobs.append(job_id),
            podcast_draft=lambda podcast_id, ws: calls.drafts.append(podcast_id),
        ),
        calls,
    )


def _generator(calls: list):
    async def generate(_session, _workspace_id, outline, *, language, kinds):
        calls.append((language, tuple(kinds), [s.heading for s in outline.sections]))
        return [
            ReportSuggestion(
                section=2, kind="quiz", title="Escalation drill", focus="f"
            ),
            ReportSuggestion(section=1, kind="slides", title="Rounds deck", focus="g"),
        ]

    return generate


async def _report_setup(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner = await make_owner()
    ws = owner.workspace.id
    source = await make_document(ws, title="PICU handbook", text="Handbook text.")
    other = await make_document(ws, title="Other source", text="Other text.")
    notebook = await make_notebook(
        ws, sources=[("document", source.id), ("document", other.id)]
    )
    made = await make_studio_output(
        notebook,
        kind="report",
        artifact_format="pdf",
        files=(("report.pdf", "application/pdf", b"%PDF-1.4 test"),),
        metadata={"generated_by": {"model": "m"}},
        title="PICU Orientation",
    )
    document = await db_session.get(Document, made.artifact.document_id)
    document.source_markdown = REPORT_MD
    made.output.options = {"format": "document"}
    made.output.source_document_ids = [source.id]
    await db_session.flush()
    return owner, notebook, made, source, other


async def _view(db_session, owner, notebook, output_id, generate):
    report = await service.load_report(db_session, notebook, output_id)
    return await service.report_view(
        db_session,
        notebook,
        report,
        kinds=routes.suggestible_kinds(),
        generate=generate,
    )


async def test_suggestions_are_made_once_per_report_version(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, source, _ = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    calls: list = []
    view = await _view(db_session, owner, notebook, made.output.id, _generator(calls))

    assert view.format == "document" and view.suggestions_status == "ready"
    assert [(s.section_index, s.kind, s.state) for s in view.suggestions] == [
        (0, "slides", "open"),
        (1, "quiz", "open"),
    ]
    assert [s.section_heading for s in view.suggestions] == ["Rounds", "Escalation"]
    assert [(s.id, s.title) for s in view.sources] == [(source.id, "PICU handbook")]
    assert calls == [
        ("en", tuple(routes.suggestible_kinds()), ["Rounds", "Escalation", "Summary"])
    ]

    # Cached with the artifact: other metadata kept, content timestamp untouched.
    artifact = await db_session.get(Artifact, made.artifact.id)
    updated_at = artifact.updated_at
    assert artifact.artifact_metadata["generated_by"] == {"model": "m"}
    assert artifact.artifact_metadata["report_view"]["generation"] == 1
    again = await _view(db_session, owner, notebook, made.output.id, _generator(calls))
    assert again.suggestions == view.suggestions and len(calls) == 1
    await db_session.refresh(artifact)
    assert artifact.updated_at == updated_at

    # A revised report (new generation) gets new suggestions.
    artifact.generation = 2
    await db_session.flush()
    revised = await _view(
        db_session, owner, notebook, made.output.id, _generator(calls)
    )
    assert len(calls) == 2 and revised.generation == 2
    assert revised.suggestions[0].id.startswith("g2")


async def test_a_failed_or_unconfigured_model_is_not_cached(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, _, _ = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )

    async def broken(*_args, **_kwargs):
        raise RuntimeError("provider down")

    view = await _view(db_session, owner, notebook, made.output.id, broken)
    assert view.suggestions_status == "unavailable" and view.suggestions == []
    artifact = await db_session.get(Artifact, made.artifact.id)
    assert "report_view" not in (artifact.artifact_metadata or {})
    calls: list = []
    retried = await _view(
        db_session, owner, notebook, made.output.id, _generator(calls)
    )
    assert retried.suggestions_status == "ready" and len(calls) == 1


async def test_interactive_reports_get_no_suggestions(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, _, _ = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    made.output.options = {"format": "interactive"}
    await db_session.flush()
    calls: list = []
    view = await _view(db_session, owner, notebook, made.output.id, _generator(calls))
    assert view.format == "interactive" and view.suggestions_status == "unavailable"
    assert calls == []


async def test_add_starts_a_studio_output_from_the_report_sources_once(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, source, _ = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    view = await _view(db_session, owner, notebook, made.output.id, _generator([]))
    quiz = next(s for s in view.suggestions if s.kind == "quiz")
    dispatchers, calls = _dispatchers()

    added = await routes.add_report_suggestion(
        owner.workspace.id,
        notebook.id,
        made.output.id,
        quiz.id,
        db_session,
        owner.auth,
        dispatchers,
    )
    item = next(s for s in added.suggestions if s.id == quiz.id)
    assert item.state == "added" and item.output_id is not None
    output = await db_session.get(NotebookOutput, item.output_id)
    assert output.kind == "quiz" and output.title == "Escalation drill"
    assert output.source_document_ids == [source.id]
    assert output.options["language"] == "English"
    assert 'section "Escalation"' in output.options["focus"]
    job = await db_session.get(DeliverableJob, output.deliverable_job_id)
    assert job.tool_call_id.endswith(f"report{made.output.id}{quiz.id}a0")
    assert "Handbook text." in job.request["prompt"]
    assert "Other text." not in job.request["prompt"]
    assert calls.jobs == [job.id]

    # Adding again while it runs starts nothing new; it is in the Studio list.
    again = await routes.add_report_suggestion(
        owner.workspace.id,
        notebook.id,
        made.output.id,
        quiz.id,
        db_session,
        owner.auth,
        dispatchers,
    )
    assert next(s for s in again.suggestions if s.id == quiz.id).output_id == output.id
    assert len(calls.jobs) == 1
    listed = await studio.get_outputs(
        owner.workspace.id, notebook.id, Response(), db_session, owner.auth
    )
    assert output.id in [o.id for o in listed]

    # Deleting the output from the Studio offers the suggestion again.
    await db_session.delete(output)
    await db_session.flush()
    reopened = await _view(db_session, owner, notebook, made.output.id, _generator([]))
    assert next(s for s in reopened.suggestions if s.id == quiz.id).state == "open"
    retry = await routes.add_report_suggestion(
        owner.workspace.id,
        notebook.id,
        made.output.id,
        quiz.id,
        db_session,
        owner.auth,
        dispatchers,
    )
    new_id = next(s for s in retry.suggestions if s.id == quiz.id).output_id
    assert new_id not in (None, output.id) and len(calls.jobs) == 2


async def test_add_falls_back_to_enabled_sources_when_the_report_ones_left(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, _, other = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    made.output.source_document_ids = [999_999]
    await db_session.flush()
    view = await _view(db_session, owner, notebook, made.output.id, _generator([]))
    dispatchers, _ = _dispatchers()
    added = await routes.add_report_suggestion(
        owner.workspace.id,
        notebook.id,
        made.output.id,
        view.suggestions[0].id,
        db_session,
        owner.auth,
        dispatchers,
    )
    output = await db_session.get(NotebookOutput, added.suggestions[0].output_id)
    assert other.id in output.source_document_ids


async def test_add_needs_the_sandbox_for_non_audio(
    db_session,
    make_owner,
    make_document,
    make_notebook,
    make_studio_output,
    monkeypatch,
):
    owner, notebook, made, _, _ = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    view = await _view(db_session, owner, notebook, made.output.id, _generator([]))
    monkeypatch.setattr(routes, "is_sandbox_enabled", lambda: False)
    dispatchers, calls = _dispatchers()
    with pytest.raises(HTTPException) as exc:
        await routes.add_report_suggestion(
            owner.workspace.id,
            notebook.id,
            made.output.id,
            view.suggestions[0].id,
            db_session,
            owner.auth,
            dispatchers,
        )
    assert exc.value.status_code == 503 and calls.jobs == []
    assert routes.suggestible_kinds() == ["audio"]


async def test_dismiss_persists_and_unknown_ids_404(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, _, _ = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    view = await _view(db_session, owner, notebook, made.output.id, _generator([]))
    assert len(view.suggestions) == 2
    assert "feedback" not in view.model_dump()
    assert not hasattr(routes, "put_report_feedback")

    dismissed = await routes.dismiss_report_suggestion(
        owner.workspace.id,
        notebook.id,
        made.output.id,
        view.suggestions[0].id,
        db_session,
        owner.auth,
    )
    assert dismissed.suggestions[0].state == "dismissed"
    again = await _view(db_session, owner, notebook, made.output.id, _generator([]))
    assert again.suggestions[0].state == "dismissed"

    with pytest.raises(HTTPException) as exc:
        await routes.dismiss_report_suggestion(
            owner.workspace.id,
            notebook.id,
            made.output.id,
            "g9s9",
            db_session,
            owner.auth,
        )
    assert exc.value.status_code == 404


async def test_another_workspace_cannot_read_or_act_on_the_report(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, _, _ = await _report_setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    stranger = await make_owner()
    with pytest.raises(HTTPException) as exc:
        await routes.get_report_view(
            owner.workspace.id,
            notebook.id,
            made.output.id,
            Response(),
            db_session,
            stranger.auth,
        )
    assert exc.value.status_code in (403, 404)

    # Its own notebook, but the output id of someone else's report.
    own = await make_notebook(stranger.workspace.id)
    with pytest.raises(HTTPException) as exc:
        await routes.get_report_view(
            stranger.workspace.id,
            own.id,
            made.output.id,
            Response(),
            db_session,
            stranger.auth,
        )
    assert exc.value.status_code == 404

    # A non-report output is not a report.
    quiz = await make_studio_output(notebook, kind="quiz", artifact_format="quiz")
    with pytest.raises(HTTPException) as exc:
        await routes.get_report_view(
            owner.workspace.id,
            notebook.id,
            quiz.output.id,
            Response(),
            db_session,
            owner.auth,
        )
    assert exc.value.status_code == 404
    assert await db_session.scalar(
        select(NotebookOutput.id).where(NotebookOutput.id == made.output.id)
    )
