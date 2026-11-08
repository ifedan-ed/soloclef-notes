"""Contract-3 producer: citation flattening.

The committed-fixture check that used to live here checked
plans/community-local/contracts/, which went with the rest of upstream
SurfSense in 7f0e2d075. The test outlived its fixture and had been failing
ever since.
"""

from __future__ import annotations

import pytest

from app.services.export_service import flatten_message_text

pytestmark = pytest.mark.unit


def test_three_citation_markers_export_with_none_and_three_titles():
    text = "A [citation:11] then [citation:22] and [citation:33] done."
    titles = {"11": "Alpha", "22": "Beta", "33": "Gamma"}
    stripped, citations = flatten_message_text(text, titles)
    assert "[citation:" not in stripped
    assert citations == [
        {"title": "Alpha"},
        {"title": "Beta"},
        {"title": "Gamma"},
    ]


def test_duplicate_citation_titles_keep_first_seen_order():
    stripped, citations = flatten_message_text(
        "See [citation:1] and again [citation:1] then [citation:2].",
        {"1": "Notes", "2": "Other"},
    )
    assert "[citation:" not in stripped
    assert citations == [{"title": "Notes"}, {"title": "Other"}]


def test_unresolved_citation_is_stripped_without_a_title():
    stripped, citations = flatten_message_text(
        "Unknown [citation:https://example.com] marker.",
        {},
    )
    assert "[citation:" not in stripped
    assert citations == []
