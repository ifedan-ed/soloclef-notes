"""Decide how an imported URL should be read and what document type it becomes."""

from __future__ import annotations

from pathlib import PurePosixPath
from urllib.parse import unquote, urlsplit

from app.db import DocumentType
from app.utils.file_extensions import DOCUMENT_EXTENSIONS

_YOUTUBE_HOSTS = frozenset(
    {"youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be"}
)
# Pages that a document parser could open but a browser renders: read by crawling.
_WEB_PAGE_EXTENSIONS = frozenset({".html", ".htm", ".xhtml", ".shtml"})


def _host(url: str) -> str:
    return (urlsplit(url).hostname or "").lower().removeprefix("www.")


def is_youtube_video(url: str) -> bool:
    parts = urlsplit(url)
    host = _host(url)
    if host == "youtu.be":
        return bool(parts.path.strip("/"))
    if host in _YOUTUBE_HOSTS:
        return parts.path == "/watch" or parts.path.startswith(("/shorts/", "/live/"))
    return False


def file_name(url: str) -> str:
    return unquote(PurePosixPath(urlsplit(url).path).name)


def is_remote_file(url: str) -> bool:
    """True when the URL names a document file (PDF, DOCX, ...) rather than a page."""
    suffix = PurePosixPath(file_name(url)).suffix.lower()
    return suffix in DOCUMENT_EXTENSIONS and suffix not in _WEB_PAGE_EXTENSIONS


def document_type_for(url: str) -> DocumentType:
    return (
        DocumentType.YOUTUBE_VIDEO
        if is_youtube_video(url)
        else DocumentType.CRAWLED_URL
    )
