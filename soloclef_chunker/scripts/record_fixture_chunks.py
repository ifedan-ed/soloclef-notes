"""Record the Python chunker's chunks for the fixture documents.

Runs in the backend container over a copy of ``testdata/documents``, writing
one ``<name>.chunks.json`` beside each ``<name>.md``. Those files are the
golden boundaries ``TestTheFixtureChunksAreThePythonOnes`` compares against,
so they are recorded from the Python chunker and never written by hand.

The fixtures are synthetic. No live document text is recorded here: this
repository has a public mirror, and the live corpus is compared by
``compare_corpus.sh``, whose output stays in a work directory.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

from app.indexing_pipeline.document_chunker import chunk_text_hybrid


def main(directory: str) -> None:
    for document in sorted(Path(directory).glob("*.md")):
        # newline="" so a CRLF fixture is chunked as CRLF: universal newlines
        # would hand the chunker a document the file does not contain.
        with document.open(encoding="utf-8", newline="") as handle:
            chunks = chunk_text_hybrid(handle.read())
        document.with_suffix(".chunks.json").write_text(
            json.dumps(chunks, ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8",
        )


if __name__ == "__main__":
    main(sys.argv[1])
