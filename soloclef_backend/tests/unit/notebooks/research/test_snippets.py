"""Snippets are short plain text, whatever shape the scraper handed back."""

from __future__ import annotations

import pytest

from app.notebooks.research.snippets import compact_count, excerpt, plain_text, trim

pytestmark = pytest.mark.unit


def test_plain_text_strips_html_and_markdown():
    raw = "<p>Hello &amp; <b>welcome</b></p>\n\n## Title\n[a link](https://x) ![img](y.png) **bold**"
    assert plain_text(raw) == "Hello & welcome Title a link bold"


def test_trim_cuts_long_text_at_a_word_boundary():
    text = "word " * 200
    out = trim(text)
    assert len(out) <= 400
    assert out.endswith("…")
    assert not out[:-1].endswith(" ")


def test_trim_leaves_short_text_alone():
    assert trim("  short   text ") == "short text"
    assert trim(None) == ""


def test_excerpt_prefers_the_paragraph_that_mentions_the_query():
    markdown = (
        "# Site nav\n\nHome | About | Contact us for more information today\n\n"
        "Solar panels convert sunlight into electricity using photovoltaic cells.\n\n"
        "Footer text that is long enough to be a paragraph but irrelevant."
    )
    assert excerpt(markdown, "how do solar panels work").startswith("Solar panels")


def test_excerpt_of_empty_page_is_empty():
    assert excerpt("", "x") == ""


@pytest.mark.parametrize(
    ("value", "text"), [(999, "999"), (1200, "1.2K"), (1_000_000, "1M"), (None, None)]
)
def test_compact_count(value, text):
    assert compact_count(value) == text
