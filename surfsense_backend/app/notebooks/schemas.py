"""Request and response shapes for the notebook API."""

from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from app.workspace_notes.readable_text import readable_title

from .chat.settings import MAX_INSTRUCTIONS_CHARS, ChatGoal, ResponseLength

_MAX_ICON_CHARS = 16


class NotebookCreate(BaseModel):
    title: str = Field(default="Untitled notebook", max_length=200)
    icon: str | None = Field(default=None, max_length=_MAX_ICON_CHARS)

    @field_validator("title")
    @classmethod
    def _title(cls, value: str) -> str:
        return value.strip() or "Untitled notebook"


class NotebookUpdate(BaseModel):
    title: str | None = Field(default=None, max_length=200)
    icon: str | None = Field(default=None, max_length=_MAX_ICON_CHARS)
    chat_goal: ChatGoal | None = None
    chat_instructions: str | None = Field(
        default=None, max_length=MAX_INSTRUCTIONS_CHARS
    )
    response_length: ResponseLength | None = None

    @field_validator("title")
    @classmethod
    def _title(cls, value: str | None) -> str | None:
        if value is None:
            return None
        cleaned = value.strip()
        if not cleaned:
            raise ValueError("title must not be blank")
        return cleaned


class NotebookRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    workspace_id: int
    title: str
    icon: str | None
    created_at: datetime
    updated_at: datetime
    source_count: int = 0
    enabled_source_count: int = 0
    chat_goal: ChatGoal = "default"
    chat_instructions: str | None = None
    response_length: ResponseLength = "default"
    featured: bool = False
    # "auto" while the notebook may still name itself; "user" once renamed.
    title_source: Literal["auto", "user"] = "user"
    # A source is indexing or just finished: the automatic name is seconds away.
    title_pending: bool = False


class NotebookCopy(BaseModel):
    title: str | None = Field(default=None, max_length=200)


SourceKind = Literal["document", "folder"]


class SourceTarget(BaseModel):
    kind: SourceKind
    id: int = Field(gt=0)


class SourcesAdd(BaseModel):
    targets: list[SourceTarget] = Field(min_length=1, max_length=500)


class SourceToggle(BaseModel):
    enabled: bool


class SourcesBulkToggle(BaseModel):
    enabled: bool
    source_ids: list[int] | None = Field(
        default=None,
        description="Sources to change; omitted means every source in the notebook",
    )


class SourceRead(BaseModel):
    id: int
    kind: SourceKind
    target_id: int
    title: str
    document_type: str | None = None
    status: str | None = None
    enabled: bool
    added_at: datetime


class NoteCreate(BaseModel):
    title: str = Field(default="New note", max_length=300)
    content: str = Field(default="", max_length=200_000)
    # "chat" when the note is an answer kept with Save to note; only the
    # automation runner writes "automation" notes.
    origin: Literal["hand", "chat"] = "hand"

    @model_validator(mode="after")
    def _answer_title_as_words(self) -> NoteCreate:
        # Save to note names the note after the answer's first line, which
        # may hold TeX ("Pearson's \(r = 0.463\)"); the web and the phone
        # write it as words now, but a phone still on an older update sends
        # it raw. A title typed by hand is kept as typed.
        if self.origin == "chat":
            self.title = readable_title(self.title)
        return self


class NoteUpdate(BaseModel):
    title: str | None = Field(default=None, max_length=300)
    content: str | None = Field(default=None, max_length=200_000)


class NoteRead(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    notebook_id: int
    title: str
    content: str
    converted_document_id: int | None
    origin: Literal["hand", "chat", "automation"] = "hand"
    automation_id: int | None = None
    created_at: datetime
    updated_at: datetime


class NoteCopy(BaseModel):
    """Where to put a copy of a note: another notebook in the same workspace."""

    notebook_id: int


class NotebookThreadRead(BaseModel):
    id: int
    title: str
    created_at: datetime
    updated_at: datetime


class SourceGuideRead(BaseModel):
    document_id: int
    title: str
    summary: str | None
    status: Literal["ready", "unavailable"]


class SearchPassage(BaseModel):
    """One matching passage: from a source document or from a note."""

    kind: Literal["source", "note"]
    title: str
    snippet: str
    # [start, end) character ranges of ``snippet`` to highlight.
    highlights: list[tuple[int, int]] = Field(default_factory=list)
    document_id: int | None = None
    note_id: int | None = None
    # The notebook source row the document reaches the notebook through (the
    # folder's row for a document inside a source folder).
    source_id: int | None = None
    folder_name: str | None = None
    document_type: str | None = None
    score: float


class SearchNotebookHit(BaseModel):
    id: int
    title: str
    icon: str | None = None
    updated_at: datetime
    passages: list[SearchPassage]


class NotebookSearchRead(BaseModel):
    query: str
    notebooks: list[SearchNotebookHit]
    took_ms: int
