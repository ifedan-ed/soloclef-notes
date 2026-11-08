"""Stamp PDF page numbers on chunks indexed before chunks carried pages.

Reads each stored PDF original's text layer with pymupdf (no parser re-run,
no model call, nothing billed) and locates every chunk in it. Dry run unless
``--apply``; safe to re-run. See app/source_pages/backfill.py.

    python scripts/backfill_chunk_pages.py                 # dry run, report only
    python scripts/backfill_chunk_pages.py --apply
    python scripts/backfill_chunk_pages.py --apply --workspace-id 3 --limit 50
    python scripts/backfill_chunk_pages.py --apply --document-id 1234 --restamp
"""

from __future__ import annotations

import argparse
import asyncio
import logging

# app.db before any feature module: it owns the ORM registry's import order.
from app.db import async_session_maker  # isort: skip
from app.source_pages.backfill import backfill_chunk_pages  # isort: skip
from app.source_pages.chunk_page_stamper import StampOutcome  # isort: skip


def _parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--apply", action="store_true", help="write the pages")
    parser.add_argument("--workspace-id", type=int)
    parser.add_argument("--document-id", type=int)
    parser.add_argument("--limit", type=int, help="at most this many documents")
    parser.add_argument(
        "--restamp",
        action="store_true",
        help="also re-locate documents whose chunks all have pages already",
    )
    return parser.parse_args()


def _print_outcome(outcome: StampOutcome) -> None:
    if outcome.skipped:
        print(f"document {outcome.document_id}: skipped ({outcome.skipped})")
    else:
        print(
            f"document {outcome.document_id}: placed {outcome.placed}/"
            f"{outcome.chunks} chunks, {outcome.changed} rows changed"
        )


async def main() -> None:
    args = _parse_args()
    async with async_session_maker() as session:
        report = await backfill_chunk_pages(
            session,
            apply=args.apply,
            workspace_id=args.workspace_id,
            document_id=args.document_id,
            include_stamped=args.restamp,
            limit=args.limit,
            on_document=_print_outcome,
        )
    mode = "applied" if args.apply else "dry run (nothing written; pass --apply)"
    print(
        f"{mode}: documents {report.documents}, stamped {report.stamped}, "
        f"chunks placed {report.placed}/{report.chunks}, rows changed "
        f"{report.changed}, failed {report.failed}, skipped {report.skipped}"
    )


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    asyncio.run(main())
