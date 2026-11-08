"""Compare two chunk dumps and say where, exactly, they disagree.

A difference is reported as the document it is in, the chunk index, and the
character offset into the document where the two chunkers first part company
-- the offset is what says whether a stored chunk would have to be re-cut.
Document text is never printed: only offsets, lengths and the few characters
around the divergence.
"""

from __future__ import annotations

import base64
import json
import sys


def read(path: str) -> dict[str, dict]:
    rows = {}
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if line:
                row = json.loads(line)
                rows[row["id"]] = row
    return rows


def decode_all(encoded: list[str]) -> list[str]:
    return [base64.standard_b64decode(value).decode("utf-8") for value in encoded]


def main(python_path: str, go_path: str) -> int:
    python_rows = read(python_path)
    go_rows = read(go_path)

    documents = chunks = matching_documents = matching_chunks = 0
    differences = []

    for document_id, row in python_rows.items():
        documents += 1
        expected = decode_all(row["chunks_b64"])
        actual = decode_all(go_rows[document_id]["chunks_b64"])
        chunks += len(expected)
        matching_chunks += sum(
            1 for a, b in zip(expected, actual, strict=False) if a == b
        )
        if expected == actual:
            matching_documents += 1
            continue
        differences.append(describe(document_id, expected, actual))

    print(f"documents compared: {documents}")
    print(f"documents identical: {matching_documents}")
    print(f"chunks compared:    {chunks}")
    print(f"chunks identical:   {matching_chunks}")
    for difference in differences:
        print(difference)
    return 0 if not differences else 1


def describe(document_id: str, expected: list[str], actual: list[str]) -> str:
    offset = 0
    for index, (a, b) in enumerate(zip(expected, actual, strict=False)):
        if a != b:
            return (
                f"document {document_id}: chunk {index} differs at character "
                f"offset {offset}: Python cut {len(a)} characters, Go cut "
                f"{len(b)}; Python ends {a[-20:]!r}, Go ends {b[-20:]!r}"
            )
        offset += len(a)
    return (
        f"document {document_id}: {len(expected)} chunks from Python, "
        f"{len(actual)} from Go; they agree up to character offset {offset}"
    )


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], sys.argv[2]))
