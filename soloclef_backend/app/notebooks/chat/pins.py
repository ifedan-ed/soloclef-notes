"""Resolve a notebook chat's enabled sources into its retrieval scope.

A chat bound to a notebook carries the notebook's enabled sources on every
turn, server-side, so the client cannot widen or forge them. Only documents
and folders that live in the turn's own workspace are returned; a stale or
foreign reference is dropped rather than trusted.

``scope_document_ids`` is the hard limit for the turn: the enabled document
sources plus the documents inside enabled folder sources (the same expansion
Studio uses). Nothing outside it is searched, listed or read.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.agents.chat.retrieval_scope import RetrievalScope, SourcesOnly
from app.agents.chat.tool_preference import ToolPreference, specialist_for
from app.db import Document, Folder, NewChatThread

from ..persistence import Notebook, NotebookSource
from .settings import ChatSettings

# The pointer block names at most this many sources; retrieval still uses all.
MAX_LISTED_SOURCES = 40


@dataclass(frozen=True)
class NotebookPins:
    notebook_id: int
    title: str
    document_ids: list[int] = field(default_factory=list)
    folder_ids: list[int] = field(default_factory=list)
    source_titles: list[str] = field(default_factory=list)
    # Enabled document sources plus the documents in enabled folder sources.
    scope_document_ids: list[int] = field(default_factory=list)
    settings: ChatSettings = field(default_factory=ChatSettings)

    @property
    def is_empty(self) -> bool:
        return not (self.document_ids or self.folder_ids)


def notebook_retrieval_scope(
    requested: RetrievalScope,
    pins: NotebookPins | None,
    *,
    preferred_tool: ToolPreference | None = None,
    mentions_connector: bool = False,
) -> tuple[RetrievalScope, SourcesOnly | None]:
    """The retrieval a turn actually runs with.

    A notebook chat answers from its enabled sources only, like NotebookLM:
    whatever scope the client sent (the composer's old Sources / + Web switch
    survives as a workspace cookie), the turn reads the notebook's sources and
    never retrieves from the web. Two deliberate, per-message picks still
    work: a tool chosen from the composer's Tools menu (only that web
    specialist exists for the turn, and its results are cited as web results)
    and an @-mentioned connected account (its app may be read). Plain chats
    (automations) keep the request as is.
    """
    if pins is None:
        return requested, None
    picked = (specialist_for(preferred_tool),) if preferred_tool is not None else ()
    scope = RetrievalScope.ALL if mentions_connector else RetrievalScope.DOCUMENTS
    return scope, SourcesOnly.picking(picked)


async def load_notebook_pins(
    session: AsyncSession, *, chat_id: int, workspace_id: int
) -> NotebookPins | None:
    """The notebook pins for this chat, or ``None`` if it is not a notebook chat."""
    notebook = await session.scalar(
        select(Notebook)
        .join(NewChatThread, NewChatThread.notebook_id == Notebook.id)
        .where(
            NewChatThread.id == chat_id,
            NewChatThread.workspace_id == workspace_id,
            Notebook.workspace_id == workspace_id,
        )
    )
    if notebook is None:
        return None
    return await _pins_for(session, notebook, workspace_id)


async def load_notebook_pins_by_id(
    session: AsyncSession, *, notebook_id: int, workspace_id: int
) -> NotebookPins | None:
    """The same pins for a notebook named directly (an automation step scoped
    to a notebook), or ``None`` if it is not a notebook in this workspace."""
    notebook = await session.scalar(
        select(Notebook).where(
            Notebook.id == notebook_id, Notebook.workspace_id == workspace_id
        )
    )
    if notebook is None:
        return None
    return await _pins_for(session, notebook, workspace_id)


async def _pins_for(
    session: AsyncSession, notebook: Notebook, workspace_id: int
) -> NotebookPins:
    doc_rows = (
        await session.execute(
            select(Document.id, Document.title)
            .join(NotebookSource, NotebookSource.document_id == Document.id)
            .where(
                NotebookSource.notebook_id == notebook.id,
                NotebookSource.enabled.is_(True),
                Document.workspace_id == workspace_id,
            )
            .order_by(NotebookSource.added_at, NotebookSource.id)
        )
    ).all()
    folder_rows = (
        await session.execute(
            select(Folder.id, Folder.name)
            .join(NotebookSource, NotebookSource.folder_id == Folder.id)
            .where(
                NotebookSource.notebook_id == notebook.id,
                NotebookSource.enabled.is_(True),
                Folder.workspace_id == workspace_id,
            )
            .order_by(NotebookSource.added_at, NotebookSource.id)
        )
    ).all()
    document_ids = [row.id for row in doc_rows]
    folder_ids = [row.id for row in folder_rows]
    scope = set(document_ids)
    if folder_ids:
        scope.update(
            (
                await session.scalars(
                    select(Document.id).where(
                        Document.workspace_id == workspace_id,
                        Document.folder_id.in_(folder_ids),
                    )
                )
            ).all()
        )
    return NotebookPins(
        notebook_id=notebook.id,
        title=notebook.title,
        document_ids=document_ids,
        folder_ids=folder_ids,
        scope_document_ids=sorted(scope),
        source_titles=[row.title for row in doc_rows]
        + [f"{row.name}/" for row in folder_rows],
        settings=ChatSettings(
            goal=notebook.chat_goal or "default",
            instructions=notebook.chat_instructions,
            length=notebook.response_length or "default",
        ),
    )
