"""How an imported URL is read: video, linked file, or web page."""

from __future__ import annotations

import pytest

from app.db import DocumentType
from app.notebooks.research.ingest.fallback import fallback_markdown
from app.notebooks.research.ingest.kind import (
    document_type_for,
    is_remote_file,
    is_youtube_video,
)
from app.notebooks.research.ingest.youtube_video import transcript_text

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("url", "video"),
    [
        ("https://www.youtube.com/watch?v=abc", True),
        ("https://youtu.be/abc", True),
        ("https://youtube.com/shorts/abc", True),
        ("https://www.youtube.com/@channel", False),
        ("https://example.com/watch?v=abc", False),
    ],
)
def test_youtube_videos_are_recognised(url, video):
    assert is_youtube_video(url) is video
    expected = DocumentType.YOUTUBE_VIDEO if video else DocumentType.CRAWLED_URL
    assert document_type_for(url) is expected


@pytest.mark.parametrize(
    ("url", "is_file"),
    [
        ("https://example.com/paper.pdf", True),
        ("https://example.com/files/Report%202024.docx", True),
        ("https://example.com/page.html", False),
        ("https://example.com/article", False),
    ],
)
def test_linked_files_are_told_apart_from_pages(url, is_file):
    assert is_remote_file(url) is is_file


def test_transcript_drops_cue_numbers_and_timestamps():
    srt = "1\n00:00:01,000 --> 00:00:02,000\nHello there\n\n2\n00:00:02,000 --> 00:00:03,000\nGeneral Kenobi\n"
    assert transcript_text(srt) == "Hello there General Kenobi"


def test_fallback_keeps_title_snippet_and_link():
    assert fallback_markdown("Solar", "A teaser", "https://e.com") == (
        "# Solar\n\nA teaser\n\nSource: https://e.com"
    )
