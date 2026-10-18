"""Query parsing, snippets, highlights and grouping for the notebooks home search."""

from __future__ import annotations

import random
import time

import pytest

from app.notebooks.search.grouping import Candidate, SourceLink, group_passages
from app.notebooks.search.text import (
    _densest_window,
    has_literal_match,
    highlight_ranges,
    make_snippet,
    plain_text,
    prefix_tsquery,
    query_terms,
)

pytestmark = pytest.mark.unit


def test_query_terms_drop_stopwords_single_letters_and_repeats():
    assert query_terms("The school NURSES and the school") == ["school", "nurses"]
    assert query_terms("p-value") == ["value"]
    assert query_terms("  ") == []


def test_query_terms_cannot_smuggle_tsquery_operators():
    terms = query_terms("asthma & !(nurse | x):* <-> foo_bar")
    assert terms == ["asthma", "nurse", "foo", "bar"]
    assert prefix_tsquery(terms) == "asthma:* & nurse:* & foo:* & bar:*"
    assert prefix_tsquery([]) is None


def test_highlights_start_at_word_boundaries_and_ignore_case():
    text = "Asthmatics and asthma; not plasma or ma."
    ranges = highlight_ranges(text, ["asthma"])
    assert [text[a:b] for a, b in ranges] == ["Asthma", "asthma"]
    # "ma" must start a word: it lights up the last word, not "asthma"/"plasma".
    assert [text[a:b] for a, b in highlight_ranges(text, ["ma"])] == ["ma"]


def test_plain_text_flattens_markdown():
    md = "# Title\n\nSome **bold** [link](http://x.y) and ![img](a.png) | cell |"
    assert plain_text(md) == "Title Some bold link and img cell"


def test_snippet_centres_on_the_densest_match_and_marks_cuts():
    filler = "lorem ipsum dolor sit amet " * 30
    content = f"{filler} school nurses face barriers in asthma care. {filler}"
    snippet, highlights = make_snippet(content, ["school", "nurses", "asthma"])
    assert snippet.startswith("… ") and snippet.endswith(" …")
    assert len(snippet) <= 260
    marked = {snippet[a:b].lower() for a, b in highlights}
    assert marked == {"school", "nurses", "asthma"}


def test_snippet_without_a_literal_match_starts_at_the_top():
    snippet, highlights = make_snippet("Wheezing in children. " * 40, ["asthma"])
    assert snippet.startswith("Wheezing in children.")
    assert highlights == []


def test_short_content_is_returned_whole():
    snippet, highlights = make_snippet("Asthma plan.", ["asthma"])
    assert snippet == "Asthma plan."
    assert highlights == [(0, 6)]


# --- time in proportion to the text (custom/README.md, "Security fixes of
# 2026-10-11", follow-up) ---------------------------------------------------

#: A note's longest text through the API (NoteCreate.content).
_LONG = 200_000


@pytest.mark.parametrize(
    "unit",
    [
        "[",  # a label never closed
        "![",  # an image's alt never closed
        "[a](",  # a destination never closed
        "](",  # destinations with no label
        "[a]",  # labels with no destination
        "<",  # a tag never closed
        "<a",
        "asthma ",  # one word, a hit every 7 characters
    ],
)
def test_a_crafted_long_note_is_snipped_in_time(unit):
    # The old patterns took ~1 s for 20,000 characters of "[" and grew with
    # the square of the length (~100 s here); the old window search took
    # ~0.6 s for 5,000 hits of one word (~30 s here).
    body = unit * (_LONG // len(unit))
    started = time.perf_counter()
    make_snippet(body, ["asthma"])
    has_literal_match(body, ["asthma"])
    assert time.perf_counter() - started < 1.0


def _densest_start_as_before(text, ranges, width):
    # The old O(hits²) window search, kept here as the reference.
    best_start, best_key = ranges[0][0], (-1, -1)
    for anchor, _ in ranges:
        window = [r for r in ranges if anchor <= r[0] and r[1] <= anchor + width]
        key = (len({text[a:b].lower() for a, b in window}), len(window))
        if key > best_key:
            best_start, best_key = anchor, key
    return best_start


def test_the_window_found_in_one_pass_is_the_one_found_before():
    rng = random.Random(20261011)
    words = ["asthma", "nurse", "nurses", "school", "lorem", "ipsum", "x" * 300]
    for _ in range(300):
        text = " ".join(rng.choice(words) for _ in range(rng.randint(1, 120)))
        terms = rng.sample(["asthma", "nurse", "school", "xxxx"], rng.randint(1, 3))
        ranges = highlight_ranges(text, terms)
        if not ranges:
            continue
        width = rng.choice([20, 60, 240])
        assert _densest_window(text, ranges, width) == _densest_start_as_before(
            text, ranges, width
        )


def test_links_and_images_still_show_their_words():
    assert plain_text("See [the guide](https://x.y/a_(b)) now") == "See the guide) now"
    assert plain_text("![chart](c.png) and [a [b](u) c") == "chart and [a b c"
    # A destination holding "]" stays as written rather than being guessed at.
    assert plain_text("[x](a]b)") == "[x](a]b)"


def _source(doc_id: int, score: float) -> Candidate:
    return Candidate("source", doc_id, f"Doc {doc_id}", "text", score)


def _note(note_id: int, notebook_id: int, score: float) -> Candidate:
    return Candidate(
        "note", note_id, f"Note {note_id}", "text", score, None, notebook_id
    )


def test_a_shared_document_appears_under_every_notebook_it_belongs_to():
    links = {1: [SourceLink(10, 100), SourceLink(20, 200, "Folder")]}
    groups = group_passages([_source(1, 0.9)], links)
    assert [
        (g.notebook_id, [p.link.source_id for p in g.passages]) for g in groups
    ] == [
        (10, [100]),
        (20, [200]),
    ]
    assert groups[1].passages[0].link.folder_name == "Folder"


def test_groups_follow_best_passage_and_prefer_distinct_items():
    links = {
        1: [SourceLink(10, 100)],
        2: [SourceLink(10, 101)],
        3: [SourceLink(30, 300)],
    }
    ranked = [
        _source(3, 0.99),
        _source(1, 0.9),
        _source(1, 0.8),  # second passage of doc 1
        _note(7, 10, 0.7),
        _source(2, 0.6),
    ]
    groups = group_passages(ranked, links)
    assert [g.notebook_id for g in groups] == [30, 10]
    nb10 = groups[1]
    # Three slots: doc 1, the note and doc 2 each get one before doc 1 gets two.
    assert [(p.candidate.kind, p.candidate.item_id) for p in nb10.passages] == [
        ("source", 1),
        ("note", 7),
        ("source", 2),
    ]


def test_second_pass_fills_remaining_slots_in_rank_order():
    links = {1: [SourceLink(10, 100)]}
    ranked = [_source(1, 0.9), _source(1, 0.8), _source(1, 0.7), _source(1, 0.6)]
    (group,) = group_passages(ranked, links)
    assert [p.candidate.score for p in group.passages] == [0.9, 0.8, 0.7]


def test_caps_on_notebooks_per_notebook_and_total():
    links = {doc: [SourceLink(doc, doc * 10)] for doc in range(1, 21)}
    ranked = [_source(doc, 1.0 - doc / 100) for doc in range(1, 21)]
    groups = group_passages(ranked, links, max_notebooks=8)
    assert len(groups) == 8
    assert [g.notebook_id for g in groups] == list(range(1, 9))

    many = {1: [SourceLink(n, n) for n in range(1, 9)]}
    ranked = [_source(1, 1.0 - i / 100) for i in range(5)]
    groups = group_passages(ranked, many, per_notebook=3, max_passages=20)
    assert sum(len(g.passages) for g in groups) == 20
    assert all(len(g.passages) <= 3 for g in groups)


def test_passages_of_documents_outside_every_notebook_are_ignored():
    assert group_passages([_source(99, 1.0)], {}) == []
