"""Name a notebook from its sources or its first question, if it may be.

Runs in the background (the ``notebook_autoname`` Celery task, or the one-time
backfill). Everything is re-read here, and the write is a compare-and-set on
``title_source = 'auto'`` and the run count, so a rename the user makes while
the model is thinking always wins, and two triggers racing name it once.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime

from sqlalchemy import case, func, select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, NewChatMessage, NewChatMessageRole, NewChatThread

from ..output_titles import retitle_outputs
from ..persistence import Notebook, NotebookSource
from .defaults import DEFAULT_TITLES
from .naming import (
    NameSuggestion,
    SourceExcerpt,
    material_language,
    question_material,
    sources_material,
    suggest_name,
)
from .policy import MAX_RUNS, AutoTitleState, Trigger, decide, next_state

logger = logging.getLogger(__name__)

Suggest = Callable[..., Awaitable[NameSuggestion | None]]

_MAX_SOURCES = 5


def _runs(column=Notebook.auto_title):
    return func.coalesce(column["runs"].as_integer(), 0)


async def ready_sources(session: AsyncSession, notebook: Notebook) -> list[Document]:
    """The notebook's ready document sources, oldest link first."""
    return list(
        (
            await session.scalars(
                select(Document)
                .join(NotebookSource, NotebookSource.document_id == Document.id)
                .where(
                    NotebookSource.notebook_id == notebook.id,
                    NotebookSource.enabled.is_(True),
                    Document.workspace_id == notebook.workspace_id,
                    Document.status["state"].astext == "ready",
                )
                .order_by(NotebookSource.added_at, NotebookSource.id)
            )
        ).all()
    )


async def first_question(session: AsyncSession, notebook_id: int) -> str | None:
    """The first question asked in any of the notebook's chats (for the backfill)."""
    content = await session.scalar(
        select(NewChatMessage.content)
        .join(NewChatThread, NewChatThread.id == NewChatMessage.thread_id)
        .where(
            NewChatThread.notebook_id == notebook_id,
            NewChatMessage.role == NewChatMessageRole.USER,
        )
        .order_by(NewChatMessage.created_at, NewChatMessage.id)
        .limit(1)
    )
    return message_text(content) or None


def message_text(content: object) -> str:
    if isinstance(content, str):
        return content.strip()
    if isinstance(content, list):
        return " ".join(
            part.get("text", "")
            for part in content
            if isinstance(part, dict) and part.get("type") == "text"
        ).strip()
    if isinstance(content, dict):
        return str(content.get("text") or "").strip()
    return ""


def _excerpt(document: Document) -> SourceExcerpt:
    return SourceExcerpt(
        title=document.title or "",
        text=(document.source_markdown or document.content or ""),
    )


async def autoname_notebook(
    session: AsyncSession,
    notebook_id: int,
    *,
    trigger: Trigger,
    question: str | None = None,
    suggest: Suggest = suggest_name,
    now: datetime | None = None,
) -> NameSuggestion | None:
    """Rename the notebook if the policy allows; the applied name, or None."""
    notebook = await session.get(Notebook, notebook_id)
    if notebook is None:
        return None
    state = AutoTitleState.of(notebook.title_source, notebook.auto_title)
    if state.title_source != "auto" or state.runs >= MAX_RUNS:
        return None

    sources = await ready_sources(session, notebook)
    question = (question or "").strip() or None
    basis = decide(
        state,
        ready_sources=len(sources),
        has_question=question is not None,
        trigger=trigger,
        now=now,
    )
    if basis is None:
        return None

    if basis == "sources":
        excerpts = [_excerpt(d) for d in sources[:_MAX_SOURCES]]
        material = sources_material(excerpts)
        language = material_language(sources=excerpts)
    else:
        material = question_material(question or "")
        language = material_language(question=question)

    workspace_id = notebook.workspace_id
    old_title = notebook.title
    # Nothing is held open while the model thinks.
    await session.commit()
    suggestion = await suggest(
        session, workspace_id, material=material, language=language
    )
    if suggestion is None:
        return None

    now = now or datetime.now(UTC)
    values: dict = {
        "title": suggestion.title,
        "auto_title": next_state(
            state.runs, basis=basis, ready_sources=len(sources), now=now
        ),
    }
    if suggestion.icon:
        values["icon"] = case(
            (Notebook.icon_source == "auto", suggestion.icon), else_=Notebook.icon
        )
    result = await session.execute(
        update(Notebook)
        .where(
            Notebook.id == notebook_id,
            Notebook.title_source == "auto",
            _runs() == state.runs,
        )
        .values(**values)
        .execution_options(synchronize_session=False)
    )
    if not result.rowcount:
        await session.commit()
        logger.info(
            "Notebook %s was renamed meanwhile; automatic title dropped", notebook_id
        )
        return None
    await retitle_outputs(
        session, notebook_id, {old_title, *DEFAULT_TITLES}, suggestion.title
    )
    await session.commit()
    logger.info(
        "Notebook %s named automatically from its %s (run %d)",
        notebook_id,
        basis,
        state.runs + 1,
    )
    return suggestion


async def notebooks_to_name(session: AsyncSession, document_id: int) -> list[int]:
    """Notebooks linking this document that may still name themselves."""
    return list(
        (
            await session.scalars(
                select(Notebook.id)
                .join(NotebookSource, NotebookSource.notebook_id == Notebook.id)
                .where(
                    NotebookSource.document_id == document_id,
                    Notebook.title_source == "auto",
                    _runs() < MAX_RUNS,
                )
                .distinct()
            )
        ).all()
    )
