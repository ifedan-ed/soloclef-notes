"""One HTTP byte range, parsed against a file's size.

Shared by the artifact and document-file routes so a browser can seek a video
or a PDF viewer can fetch only the pages it shows, without loading the file.
"""

from __future__ import annotations


def parse_range(value: str, size: int) -> tuple[int, int]:
    """Parse one byte range, returning inclusive offsets."""
    if size <= 0 or not value.startswith("bytes="):
        raise ValueError("Invalid byte range")
    first = value.removeprefix("bytes=").strip()
    if "," in first:
        raise ValueError("Multiple byte ranges are not supported")
    start_text, separator, end_text = first.partition("-")
    if separator != "-":
        raise ValueError("Invalid byte range")
    if not start_text:
        try:
            suffix_length = int(end_text)
        except ValueError:
            raise ValueError("Invalid byte range") from None
        if suffix_length <= 0:
            raise ValueError("Invalid byte range")
        return max(size - suffix_length, 0), size - 1
    try:
        start = int(start_text)
        end = int(end_text) if end_text else size - 1
    except ValueError:
        raise ValueError("Invalid byte range") from None
    if start < 0 or start >= size or end < start:
        raise ValueError("Unsatisfiable byte range")
    return start, min(end, size - 1)
