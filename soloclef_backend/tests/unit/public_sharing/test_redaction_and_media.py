from __future__ import annotations

import pytest

from app.public_sharing.inline_media import display_for, public_file_headers
from app.public_sharing.redaction import without_internal_keys

pytestmark = pytest.mark.unit


def test_internal_keys_are_dropped_at_any_depth():
    part = {
        "result": {
            "artifact_id": 5,
            "workspace_id": 2,
            "storage_key": "s3://x",
            "slides": [{"audio_file": "/srv/a.mp3", "slide_number": 1}],
        },
        "author_id": "uuid",
    }
    assert without_internal_keys(part) == {
        "result": {"artifact_id": 5, "workspace_id": 2, "slides": [{"slide_number": 1}]}
    }


@pytest.mark.parametrize(
    ("mime", "display"),
    [
        ("image/png", "image"),
        ("application/pdf", "pdf"),
        ("audio/mpeg", "audio"),
        ("video/mp4", "video"),
        ("text/html", "file"),
        ("image/svg+xml", "file"),
    ],
)
def test_only_inert_types_show_inline(mime, display):
    assert display_for(mime) == display
    headers = public_file_headers(mime, "x")
    assert ("content-disposition" in {k.lower() for k in headers}) == (
        display == "file"
    )
