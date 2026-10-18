"""Studio suggestions per output type, and follow-up questions after an answer."""

from __future__ import annotations

import pytest

from app.notebooks.routes import followups
from app.notebooks.studio.source_material import SourceDocument
from app.notebooks.studio.suggestions import (
    StudioSuggestions,
    SuggestionPool,
    focus_suggestions,
)

pytestmark = pytest.mark.integration


def _doc(doc_id: int) -> SourceDocument:
    return SourceDocument(id=doc_id, title=f"Doc {doc_id}", text="Asthma text.")


async def test_suggestions_are_cached_per_kind_and_source_set(
    db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    calls: list[str] = []

    async def generate(_session, _workspace_id, material, request):
        calls.append(request.kind)
        assert "Asthma text." in material
        kind = request.kind
        return SuggestionPool(
            ideas=[f"{kind} {n}" for n in range(9)],
            examples=[f"Example for {kind}"],
        )

    quiz = await focus_suggestions(
        db_session, notebook, [_doc(1)], kind="quiz", generate=generate
    )
    assert quiz.placeholder == "Example for quiz"
    again = await focus_suggestions(
        db_session, notebook, [_doc(1)], kind="quiz", generate=generate
    )
    assert again == quiz
    # The pool survives a reload of the notebook row.
    await db_session.refresh(notebook)
    assert notebook.focus_suggestions["quiz||"]["ideas"][0] == "quiz 0"
    audio = await focus_suggestions(
        db_session, notebook, [_doc(1)], kind="audio", generate=generate
    )
    assert audio.suggestions == ["audio 0", "audio 1", "audio 2"]
    assert len(audio.pool) == 9
    # Both kinds stay cached side by side.
    await focus_suggestions(
        db_session, notebook, [_doc(1)], kind="quiz", generate=generate
    )
    assert calls == ["quiz", "audio"]
    # A different source set regenerates.
    await focus_suggestions(
        db_session, notebook, [_doc(2)], kind="quiz", generate=generate
    )
    assert calls == ["quiz", "audio", "quiz"]


async def test_no_sources_means_no_suggestions_and_no_model_call(
    db_session, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)

    async def generate(*_args):
        raise AssertionError("must not be called")

    result = await focus_suggestions(
        db_session, notebook, [], kind="quiz", generate=generate
    )
    assert result == StudioSuggestions()


async def test_followups_see_the_answer_and_enabled_source_titles(
    db_session, make_owner, make_document, make_notebook
):
    owner = await make_owner()
    on = await make_document(owner.workspace.id, "GINA 2024")
    off = await make_document(owner.workspace.id, "Hidden paper")
    notebook = await make_notebook(
        owner.workspace.id,
        sources=[("document", on.id), ("document", off.id, False)],
    )
    seen: list[str] = []

    async def generate(_session, _workspace_id, material):
        seen.append(material)
        return ["Q1?", "Q2?", "Q3?"]

    result = await followups.get_followups(
        owner.workspace.id,
        notebook.id,
        followups.FollowupsRequest(answer="Inhaled steroids reduce exacerbations."),
        db_session,
        owner.auth,
        generate,
    )
    assert result.suggestions == ["Q1?", "Q2?", "Q3?"]
    assert "Inhaled steroids" in seen[0]
    assert "GINA 2024" in seen[0] and "Hidden paper" not in seen[0]


async def test_followups_respect_the_workspace_boundary(
    db_session, make_owner, make_notebook
):
    from fastapi import HTTPException

    owner = await make_owner()
    stranger = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    with pytest.raises(HTTPException) as exc:
        await followups.get_followups(
            owner.workspace.id,
            notebook.id,
            followups.FollowupsRequest(answer="x"),
            db_session,
            stranger.auth,
            None,
        )
    assert exc.value.status_code == 403
