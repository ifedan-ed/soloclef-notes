"""Record the durations of videos and podcasts rendered before they were stored.

Safe to re-run; see app/artifacts/media/backfill_durations.py.

    python scripts/backfill_media_durations.py
"""

from __future__ import annotations

import asyncio
import logging

# app.db before any artifact module: it owns the ORM registry's import order.
from app.db import async_session_maker  # isort: skip
from app.artifacts.media.backfill_durations import backfill_durations  # isort: skip


async def main() -> None:
    async with async_session_maker() as session:
        report = await backfill_durations(session)
    print(
        f"videos measured: {report.videos}; podcasts measured: {report.podcasts}; "
        f"skipped artifacts: {report.skipped}"
    )


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    asyncio.run(main())
