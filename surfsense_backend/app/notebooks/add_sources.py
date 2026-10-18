"""Link documents/folders onto a notebook as sources.

The one place that writes ``NotebookSource`` rows for "add this to the
notebook" (as opposed to the connector-import and research-import paths,
which have their own provenance to record). The Sources API route
(``routes/sources.py``) and the chat @-mention auto-add
(``chat/auto_add_mentions.py``) both go through this so a notebook's set of
sources is grown in exactly one way.

Does not commit; callers own the transaction.
"""

from __future__ import annotations

from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession

from .access import touch
from .autoname.task import after_sources_added
from .persistence import Notebook, NotebookSource


async def link_sources(
    session: AsyncSession,
    notebook: Notebook,
    *,
    document_ids: list[int],
    folder_ids: list[int],
) -> None:
    """Insert ``NotebookSource`` rows for ``document_ids``/``folder_ids``.

    Idempotent (``on_conflict_do_nothing``): an id already linked is a
    no-op. Callers are responsible for checking the targets belong to the
    notebook's workspace and that the caller may read them.
    """
    rows = [{"notebook_id": notebook.id, "document_id": d} for d in document_ids] + [
        {"notebook_id": notebook.id, "folder_id": f} for f in folder_ids
    ]
    if not rows:
        return
    for row in rows:
        await session.execute(
            pg_insert(NotebookSource).values(**row).on_conflict_do_nothing()
        )
    touch(notebook)
    await after_sources_added(
        session, notebook.id, notebook.title_source, document_ids
    )
