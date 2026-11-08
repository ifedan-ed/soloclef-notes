"""Move the notes automations wrote as files into their notebooks (one-off).

What it does and why: app/workspace_notes/attach_automation_notes.py.
Safe to re-run. Without --apply it only reports what it would do. Files a run
stamped are moved; older, unstamped files that only match a run's time are
listed and moved only when named with --document.

    # every automation, report only
    docker exec soloclef-backend-1 python scripts/attach_automation_notes.py
    # one automation into --notebook, taking the time matches you checked
    docker exec soloclef-backend-1 python scripts/attach_automation_notes.py \\
        --automation 3 --notebook 18 --document 199-207 --apply
"""

from __future__ import annotations

import argparse
import asyncio
import logging

# app.db before any feature module: it owns the ORM registry's import order.
from app.db import async_session_maker  # isort: skip
from app.workspace_notes.attach_automation_notes import (  # isort: skip
    AttachReport,
    attach_automation_notes,
)


def document_ids(value: str) -> list[int]:
    """``199``, ``199,200`` or ``199-207`` (both ends included)."""
    ids: list[int] = []
    for part in value.split(","):
        part = part.strip()
        if not part:
            continue
        first, dash, last = part.partition("-")
        try:
            low = int(first)
            high = int(last) if dash else low
        except ValueError:
            raise argparse.ArgumentTypeError(f"not a document id: {part!r}") from None
        if low <= 0 or high < low:
            raise argparse.ArgumentTypeError(f"not a document range: {part!r}")
        ids.extend(range(low, high + 1))
    return ids


def _print(report: AttachReport, *, apply: bool) -> None:
    verb = "" if apply else "would "
    print(
        f"Automation {report.automation_id} {report.automation_name!r} "
        f"(workspace {report.workspace_id})"
    )
    if report.notebook_id is not None:
        print(f"  notebook: {report.notebook_id} {report.notebook_title!r}")
    if report.problem:
        print(f"  note: {report.problem}")
    for note_id, title in report.notebook_notes_labelled:
        print(f"  {verb}label notebook note {note_id} {title!r}")
    for doc_id, title in report.labelled:
        print(f"  {verb}label document {doc_id} {title!r}")
    for doc_id, title in report.moved:
        print(f"  {verb}move document {doc_id} {title!r} into the notebook")
    for doc_id, title, reason in report.left:
        print(f"  left document {doc_id} {title!r} where it is: {reason}")
    for doc_id, title, reason in report.time_matches:
        print(
            f"  matched by time only, not touched: document {doc_id} {title!r} "
            f"({reason}); check it, then name it with --document {doc_id}"
        )
    for doc_id in report.not_matched:
        print(f"  document {doc_id} is not one of its time matches: not touched")
    if not (
        report.notebook_notes_labelled
        or report.labelled
        or report.moved
        or report.left
        or report.time_matches
        or report.not_matched
    ):
        print("  nothing to do")


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument(
        "--automation",
        type=int,
        action="append",
        help="automation id (repeatable); default: every automation",
    )
    parser.add_argument(
        "--notebook",
        type=int,
        help="notebook id to move the notes into, instead of the automation's "
        "'Save results to notebook' (needs one --automation)",
    )
    parser.add_argument(
        "--document",
        type=document_ids,
        action="append",
        default=[],
        help="time-matched document ids to take, after checking them: 199, "
        "199,200 or 199-207 (repeatable; needs one --automation)",
    )
    parser.add_argument(
        "--apply", action="store_true", help="make the changes (default: report only)"
    )
    args = parser.parse_args()
    named = [doc_id for group in args.document for doc_id in group]

    async with async_session_maker() as session:
        try:
            reports = await attach_automation_notes(
                session,
                automation_ids=args.automation,
                notebook_id=args.notebook,
                document_ids=named,
                apply=args.apply,
            )
        except ValueError as exc:
            parser.error(str(exc))
    for report in reports:
        _print(report, apply=args.apply)
    if not args.apply:
        print("Report only. Run again with --apply to make these changes.")


if __name__ == "__main__":
    logging.basicConfig(level=logging.WARNING)
    asyncio.run(main())
