"""Give videos rendered before 2026-09-26 their title as a file name.

Until then a Video Overview's file kept the render's working name
(``deliverable-job-62-attempt-1.mp4``), which the player handed to anyone who
downloaded from it. This renames only those files, from their document's
title, the same way new videos are named. Dry run unless ``--apply``.

    python scripts/rename_video_files.py [--apply]
"""

from __future__ import annotations

import asyncio
import sys

from sqlalchemy import text

from app.db import async_session_maker
from app.deliverables.video.executor import video_filename

WORKING_NAME = r"^deliverable-job-(\d+)-attempt-\d+\.mp4$"


async def main(apply: bool) -> None:
    async with async_session_maker() as session:
        rows = (
            await session.execute(
                text(
                    "select f.id, f.original_filename, d.title, "
                    "substring(f.original_filename from :pattern)::int as job "
                    "from artifact_files f join artifacts a on a.id = f.artifact_id "
                    "left join documents d on d.id = a.document_id "
                    "where a.format = 'video' and f.original_filename ~ :pattern"
                ),
                {"pattern": WORKING_NAME},
            )
        ).all()
        for file_id, old, title, job in rows:
            new = video_filename(title, job)
            print(f"{file_id}: {old} -> {new}")
            if apply:
                await session.execute(
                    text(
                        "update artifact_files set original_filename = :new where id = :id"
                    ),
                    {"new": new, "id": file_id},
                )
        if apply:
            await session.commit()
        print(("renamed" if apply else "dry run:"), len(rows), "file(s)")


if __name__ == "__main__":
    asyncio.run(main("--apply" in sys.argv))
