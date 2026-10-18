"""Rendered HTML -> markdown and page metadata, the way the crawler does it."""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Extracted:
    markdown: str | None
    title: str | None = None
    metadata: dict[str, str] = field(default_factory=dict)


def extract_page(html: str) -> Extracted:
    """Main content as markdown (trafilatura), else the whole body (markdownify)."""
    import trafilatura

    from app.proprietary.web_crawler.connector import markdown_of_whole_body

    markdown: str | None = None
    meta = None
    try:
        markdown = trafilatura.extract(
            html,
            output_format="markdown",
            include_comments=False,
            include_tables=True,
            include_images=True,
            include_links=True,
        )
        meta = trafilatura.extract_metadata(html)
    except Exception:
        markdown = None
    if not markdown or not markdown.strip():
        markdown = markdown_of_whole_body(html)
    metadata: dict[str, str] = {}
    title = None
    if meta is not None:
        title = meta.title or None
        for key in ("description", "author", "date"):
            value = getattr(meta, key, None)
            if value:
                metadata[key] = str(value)
        if getattr(meta, "image", None):
            metadata["image"] = str(meta.image)
    return Extracted(
        markdown=markdown.strip() if markdown and markdown.strip() else None,
        title=title,
        metadata=metadata,
    )
