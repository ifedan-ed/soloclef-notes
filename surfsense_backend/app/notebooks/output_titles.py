"""Studio output titles follow their notebook's name.

A Studio output is titled "<Kind>: <notebook title>" when it's made (the web
sends it localized). Once the notebook is renamed — by automatic naming or by
the user — outputs still carrying the old name as that suffix take the new
one; an output the user retitled differently is left alone. The output's
stored document (the ARTIFACT row a finished output is saved as, which the
viewer header, search and exports show) follows along when it still carries
the output's old title. A title sent
with a default "Untitled notebook" suffix while the notebook already has a
name (a tab opened before the rename) gets the current name instead.
"""

from __future__ import annotations

from sqlalchemy import select, text
from sqlalchemy.ext.asyncio import AsyncSession

from .autoname.defaults import DEFAULT_TITLES
from .persistence import Notebook, NotebookOutput

_SEPARATOR = ": "


def _split(title: str) -> tuple[str, str] | None:
    head, sep, tail = title.partition(_SEPARATOR)
    return (head, tail) if sep and head else None


def current_output_title(title: str, notebook: Notebook) -> str:
    """``title`` with a stale default-notebook suffix replaced by the current name."""
    parts = _split(title)
    if parts is None or notebook.title in DEFAULT_TITLES:
        return title
    head, tail = parts
    return f"{head}{_SEPARATOR}{notebook.title}" if tail in DEFAULT_TITLES else title


def renamed_output_title(title: str, old: str, new: str) -> str | None:
    """The retitled output, or None when it doesn't carry ``old`` as its name."""
    parts = _split(title)
    if parts is None or parts[1] != old:
        return None
    return f"{parts[0]}{_SEPARATOR}{new}"[:200]


async def retitle_outputs(
    session: AsyncSession, notebook_id: int, old_titles: set[str], new: str
) -> int:
    """Give the notebook's outputs named after any of ``old_titles`` the new name."""
    if not new or new in old_titles:
        return 0
    changed = 0
    outputs = await session.scalars(
        select(NotebookOutput).where(NotebookOutput.notebook_id == notebook_id)
    )
    for output in outputs:
        for old in old_titles:
            previous = output.title or ""
            retitled = renamed_output_title(previous, old, new)
            if retitled is not None:
                output.title = retitled
                changed += 1
                if output.deliverable_job_id is not None:
                    await session.execute(
                        _RETITLE_DOCUMENT,
                        {
                            "job": output.deliverable_job_id,
                            "old": previous,
                            "new": retitled,
                        },
                    )
                break
    return changed


# The document a finished deliverable was saved as, renamed only while it
# still has the output's previous title (a document retitled by hand keeps
# its own name).
_RETITLE_DOCUMENT = text(
    """
    UPDATE documents SET title = :new
    WHERE title = :old AND id IN (
        SELECT a.document_id FROM artifacts a
        JOIN deliverable_jobs j ON j.artifact_id = a.id
        WHERE j.id = :job AND a.document_id IS NOT NULL
    )
    """
)
