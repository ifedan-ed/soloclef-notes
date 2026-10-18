"""Automatic notebook naming on real Postgres: triggers, the rename rule, the backfill."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from fastapi import Response
from langchain_core.messages import AIMessage

from app.db import NewChatMessage, NewChatMessageRole, NewChatThread
from app.notebooks.autoname import backfill as backfill_module, naming, service, task
from app.notebooks.autoname.naming import NameSuggestion
from app.notebooks.persistence import Notebook, NotebookSource
from app.notebooks.routes import notebooks, sources
from app.notebooks.schemas import (
    NotebookCreate,
    NotebookUpdate,
    SourcesAdd,
    SourceTarget,
)

pytestmark = pytest.mark.integration


class FakeSuggest:
    """Stands in for the quick-tasks model; records what it was shown."""

    def __init__(
        self, title="Complications of Premature Delivery", icon="🩺", during=None
    ):
        self.title, self.icon, self.during = title, icon, during
        self.calls: list[dict] = []

    async def __call__(self, session, workspace_id, *, material, language):
        self.calls.append({"material": material, "language": language})
        if self.during is not None:
            await self.during()
        return NameSuggestion(title=self.title, icon=self.icon)


async def _create(db_session, owner, title="Untitled notebook", icon="📓"):
    return await notebooks.create_notebook(
        owner.workspace.id,
        NotebookCreate(title=title, icon=icon),
        db_session,
        owner.auth,
    )


async def _link(db_session, notebook_id, document_id):
    db_session.add(NotebookSource(notebook_id=notebook_id, document_id=document_id))
    await db_session.flush()


async def _row(db_session, notebook_id) -> Notebook:
    notebook = await db_session.get(Notebook, notebook_id)
    await db_session.refresh(notebook)
    return notebook


@pytest.fixture
def queued(monkeypatch):
    calls: list[dict] = []

    def fake_queue(notebook_id, *, trigger, question=None, countdown=0):
        calls.append(
            {"notebook_id": notebook_id, "trigger": trigger, "question": question}
        )

    monkeypatch.setattr(task, "queue_autoname", fake_queue)
    return calls


# --- creation and rename set the flag ---------------------------------------


@pytest.mark.parametrize(
    "title",
    ["Untitled notebook", "Cuaderno sin título", "未命名笔记本", "제목 없는 노트북"],
)
async def test_default_title_creates_an_auto_named_notebook(
    db_session, make_owner, title
):
    owner = await make_owner()
    created = await _create(db_session, owner, title=title)
    assert created.title_source == "auto"
    row = await _row(db_session, created.id)
    assert (row.title_source, row.icon_source) == ("auto", "auto")


async def test_chosen_title_is_the_users(db_session, make_owner):
    owner = await make_owner()
    created = await _create(db_session, owner, title="Neonatal sepsis")
    assert created.title_source == "user"


async def test_rename_route_marks_the_title_as_the_users(db_session, make_owner):
    """Header rename, the customize dialog and the mobile app all PATCH here."""
    owner = await make_owner()
    created = await _create(db_session, owner)

    # Saving the customize dialog with only a new emoji leaves the title automatic.
    same_title = await notebooks.update_notebook(
        owner.workspace.id,
        created.id,
        NotebookUpdate(title="Untitled notebook", icon="🧠"),
        db_session,
        owner.auth,
    )
    assert same_title.title_source == "auto"
    row = await _row(db_session, created.id)
    assert (row.title_source, row.icon_source) == ("auto", "user")

    renamed = await notebooks.update_notebook(
        owner.workspace.id,
        created.id,
        NotebookUpdate(title="My Premature Birth Notes"),
        db_session,
        owner.auth,
    )
    assert renamed.title_source == "user"
    listed = await notebooks.list_notebooks(
        owner.workspace.id, Response(), db_session, owner.auth
    )
    assert listed[0].title_source == "user"


# --- naming -------------------------------------------------------------------


async def test_first_ready_source_names_the_notebook(
    db_session, make_owner, make_document
):
    owner = await make_owner()
    created = await _create(db_session, owner)
    doc = await make_document(
        owner.workspace.id,
        title="complications-of-premature-delivery.pptx",
        text="Premature delivery brings respiratory distress, infection and "
        "feeding problems for the newborn.",
    )
    await _link(db_session, created.id, doc.id)
    suggest = FakeSuggest()

    named = await service.autoname_notebook(
        db_session, created.id, trigger="source", suggest=suggest
    )

    assert named is not None
    row = await _row(db_session, created.id)
    assert (row.title, row.icon) == ("Complications of Premature Delivery", "🩺")
    assert row.title_source == "auto"
    assert row.auto_title["runs"] == 1 and row.auto_title["sources"] == 1
    material = suggest.calls[0]["material"]
    assert "complications of premature delivery" in material
    assert "respiratory distress" in material
    assert suggest.calls[0]["language"] == "en"


async def test_pending_sources_do_not_count(db_session, make_owner, make_document):
    owner = await make_owner()
    created = await _create(db_session, owner)
    doc = await make_document(owner.workspace.id, title="still-parsing.pdf")
    doc.status = {"state": "processing"}
    await _link(db_session, created.id, doc.id)
    suggest = FakeSuggest()

    assert (
        await service.autoname_notebook(
            db_session, created.id, trigger="source", suggest=suggest
        )
        is None
    )
    assert suggest.calls == []
    assert (await _row(db_session, created.id)).title == "Untitled notebook"


async def test_question_names_a_notebook_without_ready_sources(db_session, make_owner):
    owner = await make_owner()
    created = await _create(db_session, owner)
    suggest = FakeSuggest(title="Respiratory Problems in Premature Infants")

    await service.autoname_notebook(
        db_session,
        created.id,
        trigger="question",
        question="What respiratory problems are linked to premature birth?",
        suggest=suggest,
    )

    row = await _row(db_session, created.id)
    assert row.title == "Respiratory Problems in Premature Infants"
    assert row.auto_title["sources"] == 0
    assert "<first_question>" in suggest.calls[0]["material"]

    # A later question does not rename it again.
    again = FakeSuggest(title="Something Else")
    await service.autoname_notebook(
        db_session,
        created.id,
        trigger="question",
        question="And infections?",
        suggest=again,
    )
    assert again.calls == []


async def test_more_sources_refine_once(db_session, make_owner, make_document):
    owner = await make_owner()
    created = await _create(db_session, owner)
    first = await make_document(
        owner.workspace.id, title="one.pdf", text="Neonatal jaundice basics."
    )
    await _link(db_session, created.id, first.id)
    await service.autoname_notebook(
        db_session,
        created.id,
        trigger="source",
        suggest=FakeSuggest(title="Neonatal Jaundice"),
    )

    second = await make_document(
        owner.workspace.id, title="two.pdf", text="Phototherapy for jaundice."
    )
    await _link(db_session, created.id, second.id)
    await service.autoname_notebook(
        db_session,
        created.id,
        trigger="source",
        suggest=FakeSuggest(title="Neonatal Jaundice and Phototherapy"),
    )
    row = await _row(db_session, created.id)
    assert row.title == "Neonatal Jaundice and Phototherapy"
    assert row.auto_title["runs"] == 2

    third = await make_document(
        owner.workspace.id, title="three.pdf", text="Kernicterus."
    )
    await _link(db_session, created.id, third.id)
    late = FakeSuggest(title="Never Applied")
    await service.autoname_notebook(
        db_session, created.id, trigger="source", suggest=late
    )
    assert late.calls == []


async def test_refinement_window_closes(db_session, make_owner, make_document):
    owner = await make_owner()
    created = await _create(db_session, owner)
    first = await make_document(owner.workspace.id, title="one.pdf")
    await _link(db_session, created.id, first.id)
    await service.autoname_notebook(
        db_session,
        created.id,
        trigger="source",
        suggest=FakeSuggest(title="First Name"),
    )
    second = await make_document(owner.workspace.id, title="two.pdf")
    await _link(db_session, created.id, second.id)
    later = FakeSuggest(title="Too Late")
    await service.autoname_notebook(
        db_session,
        created.id,
        trigger="source",
        suggest=later,
        now=datetime.now(UTC) + timedelta(hours=2),
    )
    assert later.calls == []
    assert (await _row(db_session, created.id)).title == "First Name"


async def test_user_rename_blocks_auto_naming_for_good(
    db_session, make_owner, make_document
):
    owner = await make_owner()
    created = await _create(db_session, owner)
    await notebooks.update_notebook(
        owner.workspace.id,
        created.id,
        NotebookUpdate(title="Mine"),
        db_session,
        owner.auth,
    )
    doc = await make_document(owner.workspace.id, title="source.pdf")
    await _link(db_session, created.id, doc.id)

    suggest = FakeSuggest()
    for trigger in ("source", "question", "backfill"):
        await service.autoname_notebook(
            db_session,
            created.id,
            trigger=trigger,
            question="Anything?",
            suggest=suggest,
        )
    assert suggest.calls == []
    row = await _row(db_session, created.id)
    assert (row.title, row.title_source) == ("Mine", "user")
    # Renaming it back to the default text does not make it automatic again.
    await notebooks.update_notebook(
        owner.workspace.id,
        created.id,
        NotebookUpdate(title="Untitled notebook"),
        db_session,
        owner.auth,
    )
    await service.autoname_notebook(
        db_session, created.id, trigger="source", suggest=suggest
    )
    assert suggest.calls == []
    assert (await _row(db_session, created.id)).title == "Untitled notebook"


async def test_rename_while_the_model_thinks_wins(
    db_session, make_owner, make_document
):
    owner = await make_owner()
    created = await _create(db_session, owner)
    doc = await make_document(owner.workspace.id, title="source.pdf")
    await _link(db_session, created.id, doc.id)

    async def user_renames():
        await notebooks.update_notebook(
            owner.workspace.id,
            created.id,
            NotebookUpdate(title="Typed by me"),
            db_session,
            owner.auth,
        )

    named = await service.autoname_notebook(
        db_session,
        created.id,
        trigger="source",
        suggest=FakeSuggest(during=user_renames),
    )
    assert named is None
    row = await _row(db_session, created.id)
    assert (row.title, row.title_source) == ("Typed by me", "user")


async def test_icon_the_user_picked_is_kept(db_session, make_owner, make_document):
    owner = await make_owner()
    created = await _create(db_session, owner)
    await notebooks.update_notebook(
        owner.workspace.id,
        created.id,
        NotebookUpdate(icon="🧪"),
        db_session,
        owner.auth,
    )
    doc = await make_document(owner.workspace.id, title="source.pdf")
    await _link(db_session, created.id, doc.id)
    await service.autoname_notebook(
        db_session,
        created.id,
        trigger="source",
        suggest=FakeSuggest(title="Named", icon="🩺"),
    )
    row = await _row(db_session, created.id)
    assert (row.title, row.icon) == ("Named", "🧪")


async def test_real_prompt_path_with_a_fake_model(
    db_session, make_owner, make_document, monkeypatch
):
    """suggest_name end to end: JSON reply, cleanup, icon restricted to the picker."""

    class FakeLLM:
        async def ainvoke(self, messages):
            return AIMessage(
                content='{"title": "\\"complicaciones del parto prematuro.\\"", "icon": "🦄"}'
            )

    async def fake_llm(session, workspace_id):
        return FakeLLM()

    monkeypatch.setattr(naming, "get_quick_llm", fake_llm)
    owner = await make_owner()
    created = await _create(db_session, owner, title="Cuaderno sin título", icon="📓")
    doc = await make_document(
        owner.workspace.id,
        title="parto-prematuro.pdf",
        text="El parto prematuro se asocia con complicaciones respiratorias y "
        "neurológicas en los recién nacidos, que a menudo requieren cuidados intensivos.",
    )
    await _link(db_session, created.id, doc.id)
    await service.autoname_notebook(db_session, created.id, trigger="source")
    row = await _row(db_session, created.id)
    assert (row.title, row.icon) == ("complicaciones del parto prematuro", "📓")


# --- triggers -------------------------------------------------------------------


async def test_ready_document_queues_its_auto_named_notebooks(
    db_session, make_owner, make_document, queued
):
    owner = await make_owner()
    auto = await _create(db_session, owner)
    mine = await _create(db_session, owner, title="Named by me")
    doc = await make_document(owner.workspace.id, title="shared.pdf")
    await _link(db_session, auto.id, doc.id)
    await _link(db_session, mine.id, doc.id)

    await task.after_document_ready(db_session, doc.id)
    assert [c["notebook_id"] for c in queued] == [auto.id]
    assert queued[0]["trigger"] == "source"


async def test_adding_a_ready_source_queues_naming(
    db_session, make_owner, make_document, queued
):
    owner = await make_owner()
    created = await _create(db_session, owner)
    doc = await make_document(owner.workspace.id, title="ready.pdf")
    await sources.add_sources(
        owner.workspace.id,
        created.id,
        SourcesAdd(targets=[SourceTarget(kind="document", id=doc.id)]),
        db_session,
        owner.auth,
    )
    assert [c["notebook_id"] for c in queued] == [created.id]

    pending = await make_document(owner.workspace.id, title="pending.pdf")
    pending.status = {"state": "pending"}
    other = await _create(db_session, owner)
    await sources.add_sources(
        owner.workspace.id,
        other.id,
        SourcesAdd(targets=[SourceTarget(kind="document", id=pending.id)]),
        db_session,
        owner.auth,
    )
    assert [c["notebook_id"] for c in queued] == [created.id]


async def test_question_queues_only_an_unnamed_auto_notebook(
    db_session, make_owner, queued
):
    owner = await make_owner()
    fresh = await _create(db_session, owner)
    mine = await _create(db_session, owner, title="Mine")
    named = await _create(db_session, owner)
    await service.autoname_notebook(
        db_session,
        named.id,
        trigger="question",
        question="Q?",
        suggest=FakeSuggest(title="Done"),
    )

    for notebook in (fresh, mine, named):
        await task.after_question(
            db_session, notebook.id, "What causes apnea of prematurity?"
        )
    await task.after_question(db_session, fresh.id, "   ")
    assert [(c["notebook_id"], c["trigger"]) for c in queued] == [
        (fresh.id, "question")
    ]
    assert queued[0]["question"] == "What causes apnea of prematurity?"


# --- backfill -------------------------------------------------------------------


async def test_backfill_names_only_default_auto_notebooks(
    db_session, make_owner, make_document, monkeypatch
):
    owner = await make_owner()
    from_source = await _create(db_session, owner)
    doc = await make_document(
        owner.workspace.id, title="complications-of-premature-delivery.pptx"
    )
    await _link(db_session, from_source.id, doc.id)

    from_question = await _create(db_session, owner)
    thread = NewChatThread(
        title="Chat", workspace_id=owner.workspace.id, notebook_id=from_question.id
    )
    db_session.add(thread)
    await db_session.flush()
    db_session.add(
        NewChatMessage(
            thread_id=thread.id,
            role=NewChatMessageRole.USER,
            content=[
                {
                    "type": "text",
                    "text": "What respiratory problems follow premature birth?",
                }
            ],
        )
    )
    empty = await _create(db_session, owner)
    renamed = await _create(db_session, owner)
    await notebooks.update_notebook(
        owner.workspace.id,
        renamed.id,
        NotebookUpdate(title="Kept"),
        db_session,
        owner.auth,
    )
    await db_session.flush()

    # The backfill opens its own session; ride the test's transaction instead.
    class _Maker:
        async def __aenter__(self):
            return db_session

        async def __aexit__(self, *exc):
            return False

    monkeypatch.setattr(
        backfill_module, "get_celery_session_maker", lambda: lambda: _Maker()
    )

    async def suggest(session, workspace_id, *, material, language):
        if "<first_question>" in material:
            return NameSuggestion(
                title="Respiratory Problems After Premature Birth", icon=None
            )
        return NameSuggestion(title="Complications of Premature Delivery", icon="🩺")

    results = await backfill_module.backfill(suggest=suggest)
    by_id = {nid: (old, new) for nid, old, new in results}
    mine = {from_source.id, from_question.id, empty.id, renamed.id}
    assert {k: v for k, v in by_id.items() if k in mine} == {
        from_source.id: ("Untitled notebook", "Complications of Premature Delivery"),
        from_question.id: (
            "Untitled notebook",
            "Respiratory Problems After Premature Birth",
        ),
        empty.id: ("Untitled notebook", None),
    }
    assert (await _row(db_session, renamed.id)).title == "Kept"
    # A second run finds nothing more to do for the named ones.
    again = {nid for nid, _, _ in await backfill_module.backfill(dry_run=True)}
    assert from_source.id not in again and from_question.id not in again
    assert empty.id in again


# --- the apps' "look again" hint ------------------------------------------------


async def test_title_pending_while_a_source_indexes_or_just_finished(
    db_session, make_owner, make_document
):
    owner = await make_owner()
    created = await _create(db_session, owner)
    mine = await _create(db_session, owner, title="Mine")
    indexing = await make_document(owner.workspace.id, title="big.pptx")
    indexing.status = {"state": "processing"}
    await _link(db_session, created.id, indexing.id)
    await _link(db_session, mine.id, indexing.id)

    listed = await notebooks.list_notebooks(
        owner.workspace.id, Response(), db_session, owner.auth
    )
    pending = {n.id: n.title_pending for n in listed}
    assert pending[created.id] is True
    assert pending[mine.id] is False  # the user named it; nothing is coming

    # Ready moments ago: still pending until the name lands...
    # As the indexing pipeline does when it marks a document ready.
    indexing.status = {"state": "ready"}
    indexing.updated_at = datetime.now(UTC)
    await db_session.flush()
    detail = await notebooks.get_notebook(
        owner.workspace.id, created.id, Response(), db_session, owner.auth
    )
    assert detail.title_pending is True
    await service.autoname_notebook(
        db_session, created.id, trigger="source", suggest=FakeSuggest(title="Big Deck")
    )
    # ...and not once the name used that source.
    detail = await notebooks.get_notebook(
        owner.workspace.id, created.id, Response(), db_session, owner.auth
    )
    assert (detail.title, detail.title_pending) == ("Big Deck", False)


async def test_nothing_pending_without_sources_or_long_after(
    db_session, make_owner, make_document
):
    from app.notebooks.autoname.pending import naming_pending

    owner = await make_owner()
    created = await _create(db_session, owner)
    notebook = await db_session.get(Notebook, created.id)
    assert await naming_pending(db_session, [notebook]) == set()

    doc = await make_document(owner.workspace.id, title="old.pdf")
    await _link(db_session, created.id, doc.id)
    later = datetime.now(UTC) + timedelta(hours=1)
    assert await naming_pending(db_session, [notebook], now=later) == set()
