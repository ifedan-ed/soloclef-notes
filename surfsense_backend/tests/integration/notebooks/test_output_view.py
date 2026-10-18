"""A Studio output's viewer frame on real Postgres: prompt, sources, the
retired rating left alone, and isolation between notebooks and workspaces."""

from __future__ import annotations

import pytest
from fastapi import HTTPException, Response

from app.app import app
from app.artifacts.persistence import Artifact
from app.db import DeliverableJobStatus
from app.notebooks.routes import output_view as routes

pytestmark = pytest.mark.integration


async def _setup(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner = await make_owner()
    ws = owner.workspace.id
    used = await make_document(ws, title="Cardiology notes", text="Text.")
    other = await make_document(ws, title="Unused source", text="Other.")
    notebook = await make_notebook(
        ws, sources=[("document", used.id), ("document", other.id)]
    )
    made = await make_studio_output(
        notebook,
        kind="quiz",
        artifact_format="quiz",
        metadata={"generated_by": {"model": "m"}},
        title="Heart quiz",
    )
    made.output.options = {
        "kind": "quiz",
        "focus": "  Focus on murmurs  ",
        "count": "fewer",
    }
    made.output.source_document_ids = [used.id]
    await db_session.flush()
    return owner, notebook, made, used


async def _get(db_session, who, ws, nb, output_id):
    return await routes.get_output_view(
        ws, nb, output_id, Response(), db_session, who.auth
    )


async def test_view_shows_the_prompt_and_the_sources_it_was_made_from(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, used = await _setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    view = await _get(
        db_session, owner, owner.workspace.id, notebook.id, made.output.id
    )
    assert view.kind == "quiz"
    assert view.prompt == "Focus on murmurs"
    assert [(s.id, s.title) for s in view.sources] == [(used.id, "Cardiology notes")]
    assert not hasattr(view, "feedback")


async def test_the_rating_is_gone_and_old_ratings_are_left_as_they_were(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    """The Good/Bad content rating was removed: no route writes it, the view
    no longer carries it, and a rating stored before stays in the metadata."""
    paths = {getattr(route, "path", "") for route in app.routes}
    assert not any(path.endswith("/feedback") for path in paths)
    assert not any(path.endswith("/report-feedback") for path in paths)
    assert not hasattr(routes, "put_output_feedback")

    owner, notebook, made, _ = await _setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    artifact = await db_session.get(Artifact, made.artifact.id)
    old = {"generation": 1, "feedback": "good"}
    artifact.artifact_metadata = {**artifact.artifact_metadata, "output_view": old}
    await db_session.flush()

    view = await _get(
        db_session, owner, owner.workspace.id, notebook.id, made.output.id
    )
    assert "feedback" not in view.model_dump()
    await db_session.refresh(artifact)
    assert artifact.artifact_metadata["output_view"] == old


async def test_unready_foreign_and_unknown_outputs_are_not_found(
    db_session, make_owner, make_document, make_notebook, make_studio_output
):
    owner, notebook, made, _ = await _setup(
        db_session, make_owner, make_document, make_notebook, make_studio_output
    )
    ws = owner.workspace.id
    running = await make_studio_output(
        notebook,
        kind="quiz",
        artifact_format="quiz",
        status=DeliverableJobStatus.RUNNING,
    )
    for output_id in (running.output.id, 987654321):
        with pytest.raises(HTTPException) as exc:
            await _get(db_session, owner, ws, notebook.id, output_id)
        assert exc.value.status_code == 404

    # Another notebook of the same workspace cannot reach it by id.
    sibling = await make_notebook(ws)
    with pytest.raises(HTTPException) as exc:
        await _get(db_session, owner, ws, sibling.id, made.output.id)
    assert exc.value.status_code == 404

    # Another workspace cannot read it, from its own notebook either.
    stranger = await make_owner()
    with pytest.raises(HTTPException) as exc:
        await _get(db_session, stranger, ws, notebook.id, made.output.id)
    assert exc.value.status_code in (403, 404)
    own = await make_notebook(stranger.workspace.id)
    with pytest.raises(HTTPException) as exc:
        await _get(db_session, stranger, stranger.workspace.id, own.id, made.output.id)
    assert exc.value.status_code == 404
