"""Response and request shapes for the Notes page API."""

from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel


class NotebookRef(BaseModel):
    id: int
    title: str
    icon: str | None = None


class FolderRef(BaseModel):
    """A folder of the documents tree, as the Notes page groups by it."""

    id: int
    name: str
    # Folder names from the top of the tree down to this one.
    path: list[str]
    # Its place in the tree, depth first; the page orders folder groups by it.
    tree_order: int


class AutomationRef(BaseModel):
    # ``None`` when the automation was deleted after writing the note.
    id: int | None
    name: str


class WorkspaceNoteRead(BaseModel):
    """One row of the Notes page.

    ``kind`` says which id this is: a ``notebook_note`` id (open, edit and
    delete it through its notebook's note routes) or a ``document`` id (a note
    document: edit it through the document editor routes, delete it through
    ``DELETE /workspaces/{id}/notes/{id}``, or move it into a notebook).
    """

    kind: Literal["notebook_note", "document"]
    id: int
    title: str
    preview: str
    origin: Literal["hand", "chat", "automation"]
    # The notebook the note lives in (notebook notes only).
    notebook: NotebookRef | None = None
    # Notebooks that use it as a source: a converted notebook note's own
    # notebook, or the notebooks a note document was added to.
    source_in: list[NotebookRef] = []
    # The folder a note document sits in; ``None`` at the top of the tree,
    # and for notebook notes, which the page groups by ``notebook``.
    folder: FolderRef | None = None
    automation: AutomationRef | None = None
    created_at: datetime
    updated_at: datetime


class MoveIntoNotebook(BaseModel):
    notebook_id: int
