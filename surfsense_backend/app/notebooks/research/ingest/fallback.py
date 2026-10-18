"""Content for an imported source whose page could not be read."""

from __future__ import annotations


def fallback_markdown(title: str, snippet: str | None, url: str) -> str:
    """Title, the search snippet and the link, so the source is still citable."""
    parts = [f"# {title}"]
    if snippet and snippet.strip():
        parts.append(snippet.strip())
    parts.append(f"Source: {url}")
    return "\n\n".join(parts)
