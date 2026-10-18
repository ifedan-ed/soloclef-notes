"""One-time naming of the notebooks that predate automatic naming.

    python -m app.notebooks.autoname.backfill            # rename
    python -m app.notebooks.autoname.backfill --dry-run  # list candidates only

Only notebooks still marked ``title_source = 'auto'`` whose title is exactly a
default ("Untitled notebook" in any UI language) and that were never named
automatically are touched. Each is named from its ready sources, else from the
first question asked in its chats; one with neither keeps its title. Safe to
run again: a named notebook no longer qualifies.
"""

from __future__ import annotations

import argparse
import asyncio
import logging

from sqlalchemy import func, select

# app.db first: run as ``python -m``, nothing has loaded the models yet, and
# importing the notebook models before app.db is a circular import.
import app.db  # noqa: F401
from app.tasks.celery_tasks import get_celery_session_maker

from ..persistence import Notebook
from .defaults import DEFAULT_TITLES
from .service import autoname_notebook, first_question

logger = logging.getLogger(__name__)


async def backfill(
    *, dry_run: bool = False, suggest=None
) -> list[tuple[int, str, str | None]]:
    """``(notebook id, old title, new title or None)`` for every candidate."""
    results: list[tuple[int, str, str | None]] = []
    async with get_celery_session_maker()() as session:
        rows = (
            await session.execute(
                select(Notebook.id, Notebook.title)
                .where(
                    Notebook.title_source == "auto",
                    Notebook.title.in_(DEFAULT_TITLES),
                    func.coalesce(Notebook.auto_title["runs"].as_integer(), 0) == 0,
                )
                .order_by(Notebook.id)
            )
        ).all()
        for notebook_id, title in rows:
            if dry_run:
                results.append((notebook_id, title, None))
                continue
            try:
                question = await first_question(session, notebook_id)
                kwargs = {"suggest": suggest} if suggest else {}
                named = await autoname_notebook(
                    session,
                    notebook_id,
                    trigger="backfill",
                    question=question,
                    **kwargs,
                )
            except Exception:
                logger.warning(
                    "Backfill of notebook %s failed", notebook_id, exc_info=True
                )
                await session.rollback()
                named = None
            results.append((notebook_id, title, named.title if named else None))
    return results


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO)
    for notebook_id, old, new in asyncio.run(backfill(dry_run=args.dry_run)):
        if args.dry_run:
            print(f"{notebook_id}\t{old}\t(candidate)")
        else:
            print(f"{notebook_id}\t{old}\t->\t{new or '(unchanged)'}")


if __name__ == "__main__":
    main()
