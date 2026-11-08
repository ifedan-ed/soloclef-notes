"""Chunk every stored document with the Python chunker, inside the backend.

Runs in the backend container, where the installed chonkie and chonkie-core
are the ones that cut the chunks now in the database. It only reads: one
SELECT, no transaction of its own, and its output goes to a file.

Each line of the output is one document: its id, its ``source_markdown``, and
the chunks ``chunk_text_hybrid`` cuts from it. Text is base64-encoded so the
comparison never depends on two JSON encoders escaping a character the same
way.
"""

from __future__ import annotations

import asyncio
import base64
import json
import sys

from sqlalchemy import text

from app.db import async_session_maker
from app.indexing_pipeline.document_chunker import chunk_text_hybrid


def encode(value: str) -> str:
    return base64.standard_b64encode(value.encode("utf-8")).decode("ascii")


async def main(out_path: str) -> None:
    async with async_session_maker() as session:
        rows = (
            await session.execute(
                text(
                    "SELECT id, source_markdown FROM documents "
                    "WHERE source_markdown IS NOT NULL ORDER BY id"
                )
            )
        ).all()

    with open(out_path, "w", encoding="utf-8") as out:
        for document_id, markdown in rows:
            out.write(
                json.dumps(
                    {
                        "id": str(document_id),
                        "text_b64": encode(markdown),
                        "chunks_b64": [encode(c) for c in chunk_text_hybrid(markdown)],
                    }
                )
                + "\n"
            )


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1]))
