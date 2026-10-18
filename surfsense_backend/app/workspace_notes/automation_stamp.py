"""Say which automation wrote a note document, at the moment it is written.

An automation run's agent may write its result as a Markdown file in
``/documents`` (told to "save the summary as a note" with no notebook to save
into). So the Notes page can say "By <automation>" and the attach script can
find that automation's notes, the run puts a stamp in its agent's
``configurable`` under ``CONFIG_KEY`` (``agent_task/invoke.py``). The agent's
end-of-turn persistence reads it back (``current_stamp``) and writes it into
the metadata of each note document that turn creates: ``automation_id``,
``automation_name`` and ``automation_run_id``.

Only what the run's own agent turn creates is stamped: a note written in a
chat at the same minute, or by another automation running at the same time,
carries its own thread's config and no stamp from this run. Limits:

* Only files the turn's end-of-turn persistence creates (the agent's file
  tools) are stamped; a CSV the ``export_run`` tool saves directly is not,
  and a file the run only edits (it existed before) keeps what it said.
* On a workspace using the git knowledge store, the stamp goes on the rows the
  turn's commit projects. If the projection stands aside (a rebuild holds the
  index lock), the indexer writes those rows later without it, and the note
  shows as "From a chat".
* A step that times out or is cancelled before its agent finishes persists no
  files at all, so there is nothing to stamp.
* Notes written before the stamp existed have none; the attach script lists
  them as matched by time only and moves one only when it is named
  (``scripts/attach_automation_notes.py``, custom/README.md).
"""

from __future__ import annotations

from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentType

from .note_documents import (
    AUTOMATION_ID_KEY,
    AUTOMATION_NAME_KEY,
    AUTOMATION_RUN_KEY,
)

# Server-side only: chat turns build their config from the request without it.
CONFIG_KEY = "surfsense_automation_note_stamp"


def note_stamp(
    *, automation_id: int, automation_name: str, run_id: int | None
) -> dict[str, Any]:
    """The metadata a run's note documents get."""
    stamp: dict[str, Any] = {
        AUTOMATION_ID_KEY: automation_id,
        AUTOMATION_NAME_KEY: automation_name,
    }
    if run_id is not None:
        stamp[AUTOMATION_RUN_KEY] = run_id
    return stamp


def stamp_of(config: dict[str, Any] | None) -> dict[str, Any] | None:
    """The stamp in a run config, or ``None`` (a chat, or a malformed value)."""
    value = ((config or {}).get("configurable") or {}).get(CONFIG_KEY)
    if not isinstance(value, dict):
        return None
    automation_id = value.get(AUTOMATION_ID_KEY)
    if isinstance(automation_id, bool) or not isinstance(automation_id, int):
        return None
    return dict(value)


def current_stamp() -> dict[str, Any] | None:
    """The stamp of the graph run this is called from, if it is an automation's."""
    try:
        from langgraph.config import get_config

        config = get_config()
    except Exception:
        return None
    return stamp_of(config if isinstance(config, dict) else None)


async def stamp_documents(
    session: AsyncSession, document_ids: list[int], stamp: dict[str, Any]
) -> list[int]:
    """Add ``stamp`` to these note documents' metadata; return the ids stamped.

    For rows another path already created (the git knowledge store's commit
    projection). A document that already names an automation keeps it.
    Flushes but does not commit: the caller owns the transaction.
    """
    if not document_ids:
        return []
    documents = (
        await session.scalars(
            select(Document).where(
                Document.id.in_(document_ids),
                Document.document_type == DocumentType.NOTE,
            )
        )
    ).all()
    stamped: list[int] = []
    for document in documents:
        meta = dict(document.document_metadata or {})
        if meta.get(AUTOMATION_ID_KEY) is not None:
            continue
        meta.update(stamp)
        document.document_metadata = meta
        stamped.append(document.id)
    if stamped:
        await session.flush()
    return stamped
