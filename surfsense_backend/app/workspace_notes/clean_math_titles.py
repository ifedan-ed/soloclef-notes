"""One-off: note titles that kept an answer's TeX, written as words.

Until 2026-10-10, Save to note (web and phone) cut a note's title from the
answer's first line as written, so an answer with maths gave titles such as
"The correlation was moderate and positive: Pearson's \\(r = 0.463\\),
statistical…". This finds every notebook note and every note document
(``documents`` of type ``NOTE``) whose title holds a TeX delimiter (``\\(``,
``\\)``, ``\\[``, ``\\]``, ``$``, ``\\begin{``) and gives it its title with
the maths as words (``readable_text.readable_title``). A note kept from a
chat whose title is still the one Save to note gave it (its words are where
its text starts) is named again from its text, as Save to note names one
now, so the cut at 80 characters falls where it would today ("…Pearson's
r = 0.463, statistically s…"). A title that would read the same, money such
as "$3 and $5" included, is left alone. Note texts are not touched: the
editors draw their maths now.

Nothing is written without ``apply``. The script writes every old title to a
backup file before applying, and ``restore`` puts the titles of a backup
back (only where a title is still the one this wrote). Run through
``scripts/clean_note_title_math.py``.
"""

from __future__ import annotations

import re
from dataclasses import asdict, dataclass
from typing import Literal

from sqlalchemy import or_, select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentType
from app.notebooks.persistence import Notebook, NotebookNote

from .readable_text import first_line_of_words, readable_title

Table = Literal["notebook_notes", "documents"]

# What a title holding maths has in it (a lone "$" may be money: the new
# title decides, and an unchanged one is not listed).
_TEX_MARKS = ("\\(", "\\)", "\\[", "\\]", "$", "\\begin{")

# The web's Save to note (surfsense_web/features/notebooks/lib/
# note-from-answer.ts): its citation markers, the spaces it folds, its cut.
_CITATION = re.compile(
    r"[\[【]\u200b?citation:\s*(https?://[^\]】\u200b]+|urlcite\d+|run_[0-9a-fA-F-]+"
    r"|(?:doc-)?-?\d+(?:\s*,\s*(?:doc-)?-?\d+)*)\s*\u200b?[\]】]"
)
_SPACE_BEFORE_PUNCTUATION = re.compile(r"[ \t]+([.,;:!?])")
_SPACES = re.compile(r"[ \t]{2,}")
_ANSWER_TITLE_CHARS = 80
_CUT = "…"


@dataclass(frozen=True)
class TitleChange:
    table: Table
    id: int
    workspace_id: int
    old: str
    new: str
    # "text": named again from the note's text; "title": its own title, as words.
    source: Literal["text", "title"]


def title_from_answer(markdown: str) -> str:
    """The title Save to note gives an answer today."""
    content = _CITATION.sub("", markdown or "")
    content = _SPACES.sub(" ", _SPACE_BEFORE_PUNCTUATION.sub(r"\1", content)).strip()
    line = first_line_of_words(content)
    if len(line) > _ANSWER_TITLE_CHARS:
        return line[: _ANSWER_TITLE_CHARS - 1].rstrip() + _CUT
    return line


def new_title_for(
    title: str, *, origin: str | None, content: str | None
) -> tuple[str, str]:
    """The title a note should have, and where it came from ("text" or "title")."""
    as_words = readable_title(title)
    if origin == "chat" and content:
        from_text = title_from_answer(content)
        start = as_words.removesuffix(_CUT).rstrip()
        if start and from_text.removesuffix(_CUT).startswith(start):
            return from_text, "text"
    return as_words, "title"


def _holds_tex(column):
    return or_(*(column.contains(mark, autoescape=True) for mark in _TEX_MARKS))


async def find_title_changes(session: AsyncSession) -> list[TitleChange]:
    """Every note title holding TeX whose title as words differs; reads only."""
    changes: list[TitleChange] = []
    notes = await session.execute(
        select(
            NotebookNote.id,
            NotebookNote.title,
            NotebookNote.content,
            NotebookNote.origin,
            Notebook.workspace_id,
        )
        .join(Notebook, Notebook.id == NotebookNote.notebook_id)
        .where(_holds_tex(NotebookNote.title))
        .order_by(NotebookNote.id)
    )
    for row in notes.all():
        new, source = new_title_for(row.title, origin=row.origin, content=row.content)
        if new and new != row.title:
            changes.append(
                TitleChange(
                    "notebook_notes", row.id, row.workspace_id, row.title, new, source
                )
            )
    documents = await session.execute(
        select(Document.id, Document.title, Document.workspace_id)
        .where(Document.document_type == DocumentType.NOTE, _holds_tex(Document.title))
        .order_by(Document.id)
    )
    for row in documents.all():
        new = readable_title(row.title)
        if new and new != row.title:
            changes.append(
                TitleChange(
                    "documents", row.id, row.workspace_id, row.title, new, "title"
                )
            )
    return changes


def _model(table: Table):
    return NotebookNote if table == "notebook_notes" else Document


async def _set_title(
    session: AsyncSession, table: Table, row_id: int, *, was: str, to: str
) -> bool:
    """Set one title, only where it is still ``was`` (an edit since is kept)."""
    model = _model(table)
    result = await session.execute(
        update(model)
        .where(model.id == row_id, model.title == was)
        # Its own edit time kept: a title written as words is not an edit,
        # and the Studio panel orders notes by it.
        .values(title=to, updated_at=model.updated_at)
        .execution_options(synchronize_session=False)
    )
    return bool(result.rowcount)


async def apply_title_changes(session: AsyncSession, changes: list[TitleChange]) -> int:
    """Write the new titles in one transaction; how many were written."""
    written = 0
    for change in changes:
        written += await _set_title(
            session, change.table, change.id, was=change.old, to=change.new
        )
    await session.commit()
    return written


async def restore_titles(session: AsyncSession, backup: list[dict]) -> int:
    """Put a backup's old titles back, where a title is still the one written."""
    restored = 0
    for row in backup:
        restored += await _set_title(
            session, row["table"], int(row["id"]), was=row["new"], to=row["old"]
        )
    await session.commit()
    return restored


def backup_rows(changes: list[TitleChange]) -> list[dict]:
    return [asdict(change) for change in changes]
