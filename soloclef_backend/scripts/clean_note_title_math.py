"""Write note titles that kept an answer's TeX as words (one-off).

What it does and why: app/workspace_notes/clean_math_titles.py. Without
--apply it only reads (in a read-only transaction) and lists each title it
would change. --apply needs --backup: the old titles go to that JSON file
first, then every change is made in one transaction. --restore FILE puts a
backup's titles back. Safe to re-run: a title already as words is not listed.

    # 1. report only
    docker exec soloclef-backend-1 python scripts/clean_note_title_math.py
    # 2. back up and apply, then copy the backup off the container
    docker exec soloclef-backend-1 python scripts/clean_note_title_math.py \\
        --apply --backup /tmp/note-title-math-backup.json
    docker cp soloclef-backend-1:/tmp/note-title-math-backup.json .
    # undo, if ever needed
    docker exec soloclef-backend-1 python scripts/clean_note_title_math.py \\
        --restore /tmp/note-title-math-backup.json
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
from pathlib import Path

from sqlalchemy import text

# app.db before any feature module: it owns the ORM registry's import order.
from app.db import async_session_maker  # isort: skip
from app.workspace_notes.clean_math_titles import (  # isort: skip
    TitleChange,
    apply_title_changes,
    backup_rows,
    find_title_changes,
    restore_titles,
)

_NAMES = {"notebook_notes": "notebook note", "documents": "note document"}


def _print(changes: list[TitleChange], *, apply: bool) -> None:
    verb = "" if apply else "would "
    for change in changes:
        how = " (named again from its text)" if change.source == "text" else ""
        print(
            f"{verb}retitle {_NAMES[change.table]} {change.id} "
            f"(workspace {change.workspace_id}){how}:\n"
            f"    {change.old!r}\n -> {change.new!r}"
        )
    notes = sum(1 for c in changes if c.table == "notebook_notes")
    print(
        f"{len(changes)} title(s) {verb}change: {notes} notebook note(s), "
        f"{len(changes) - notes} note document(s)."
    )


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument(
        "--apply", action="store_true", help="make the changes (default: report only)"
    )
    parser.add_argument(
        "--backup",
        type=Path,
        help="JSON file the old titles are written to before --apply (required with it)",
    )
    parser.add_argument(
        "--restore", type=Path, help="put the titles in this backup file back"
    )
    args = parser.parse_args()
    if args.apply and args.backup is None:
        parser.error("--apply needs --backup FILE")
    if args.apply and args.restore is not None:
        parser.error("--apply and --restore do not go together")

    async with async_session_maker() as session:
        if args.restore is not None:
            backup = json.loads(args.restore.read_text(encoding="utf-8"))
            restored = await restore_titles(session, backup)
            print(f"restored {restored} of {len(backup)} title(s) from {args.restore}")
            return
        if not args.apply:
            await session.execute(text("SET TRANSACTION READ ONLY"))
        changes = await find_title_changes(session)
        if args.apply and changes:
            args.backup.write_text(
                json.dumps(backup_rows(changes), ensure_ascii=False, indent=1),
                encoding="utf-8",
            )
            print(f"old titles written to {args.backup}")
            written = await apply_title_changes(session, changes)
            _print(changes, apply=True)
            print(f"{written} written (a title edited since the report is kept).")
        else:
            _print(changes, apply=False)
            await session.rollback()


if __name__ == "__main__":
    logging.basicConfig(level=logging.WARNING)
    asyncio.run(main())
