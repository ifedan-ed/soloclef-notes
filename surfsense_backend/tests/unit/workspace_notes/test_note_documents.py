"""What a note document's row says about where it came from."""

from __future__ import annotations

import pytest

from app.workspace_notes.note_documents import (
    PREVIEW_CHARS,
    automation_id_of,
    display_title,
    origin_of,
    preview_of,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("metadata", "origin"),
    [
        ({"virtual_path": "/documents/notes/a.md", "automation_id": 3}, "automation"),
        ({"automation_name": "Daily digest"}, "automation"),
        ({"virtual_path": "/documents/notes/a.md"}, "chat"),
        ({"NOTE": True}, "hand"),
        (None, "hand"),
    ],
)
def test_origin(metadata, origin):
    assert origin_of(metadata) == origin


@pytest.mark.parametrize("bad", [None, 0, -1, True, "3", 2.5])
def test_only_a_positive_integer_is_an_automation_id(bad):
    assert automation_id_of({"automation_id": bad}) is None
    assert automation_id_of({"automation_id": 7}) == 7


@pytest.mark.parametrize(
    ("title", "shown"),
    [
        ("Email_Summary_2026-10-09.md", "Email_Summary_2026-10-09"),
        ("Plan.MD", "Plan"),
        ("Key findings", "Key findings"),
        (".md", ".md"),
        ("  ", "Untitled note"),
    ],
)
def test_display_title_drops_the_markdown_extension(title, shown):
    assert display_title(title) == shown


def test_preview_strips_markdown_and_is_cut_with_an_ellipsis():
    assert preview_of("# Inbox\n\n- **Ann**: lunch?\n") == "Inbox - Ann: lunch?"
    assert preview_of(None) == ""
    long = preview_of("word " * 200)
    assert len(long) == PREVIEW_CHARS and long.endswith("…")
