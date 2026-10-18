"""Page assignment: which page of a source each chunk's text came from."""

import pytest

from app.source_pages.page_locator import (
    PageSpan,
    assign_pages,
    fill_between_agreeing_neighbours,
    locate_chunk_pages,
    words,
)

pytestmark = pytest.mark.unit

HEADER = "Journal of Paediatric Practice volume twelve issue four"

PAGES = [
    f"{HEADER}\nAcute otitis media is the most common bacterial infection in "
    "young children and presents with ear pain and fever.",
    f"{HEADER}\nFirst line treatment is high dose amoxicillin for ten days in "
    "children younger than two years of age.",
    f"{HEADER}\nTympanostomy tubes are considered after three episodes in six "
    "months or four episodes within one year.",
]


def test_each_chunk_lands_on_the_page_its_text_is_on():
    chunks = [
        "## Treatment\n\nFirst line treatment is **high dose amoxicillin** for ten days",
        "Tympanostomy tubes are considered after three episodes in six months",
        "Acute otitis media is the most common bacterial infection in young children",
    ]

    assert locate_chunk_pages(chunks, PAGES) == [
        PageSpan(2, 2),
        PageSpan(3, 3),
        PageSpan(1, 1),
    ]


def test_a_chunk_over_a_page_break_gets_the_range():
    chunk = (
        "presents with ear pain and fever. First line treatment is high dose "
        "amoxicillin for ten days in children younger than two years of age."
    )

    assert locate_chunk_pages([chunk], PAGES) == [PageSpan(1, 2)]


def test_a_running_header_does_not_decide_the_page():
    # The header shingles are on every page, so they must not vote; the body
    # text decides.
    chunk = f"{HEADER} Tympanostomy tubes are considered after three episodes"

    assert locate_chunk_pages([chunk], PAGES) == [PageSpan(3, 3)]


def test_text_found_nowhere_gets_no_page():
    chunk = "The image shows an axial CT slice with opacification of the mastoid."

    assert locate_chunk_pages([chunk], PAGES) == [None]


def test_markdown_tables_and_text_layer_hyphenation_still_match():
    markdown_chunk = (
        "| Drug | Dose |\n|---|---|\n| Amoxicillin | 90 mg per kg per day |\n"
        "| Azithromycin | 10 mg per kg once |"
    )
    text_layer = [
        "Unrelated opening page about vaccination schedules for infants.",
        "Drug\nDose\nAmoxicillin\n90 mg per kg per day\nAzithro-\nmycin\n"
        "10 mg per kg once",
    ]

    assert locate_chunk_pages([markdown_chunk], text_layer) == [PageSpan(2, 2)]


def test_repeated_text_goes_to_the_copy_nearest_the_previous_chunk():
    repeated = "Key point: always reassess the child after the first dose given."
    pages = [
        f"Opening summary. {repeated}",
        "Middle chapter about hydration and oral rehydration solution volumes.",
        "Late chapter about discharge planning and safety netting advice.",
        f"Late recap. {repeated}",
    ]
    chunks = [
        "Late chapter about discharge planning and safety netting advice.",
        repeated,
    ]

    assert locate_chunk_pages(chunks, pages) == [PageSpan(3, 3), PageSpan(4, 4)]


def test_no_page_text_places_nothing():
    assert locate_chunk_pages(["anything at all here"], ["", "  "]) == [None]


def test_an_unplaced_chunk_between_two_chunks_of_one_page_joins_that_page():
    spans = [PageSpan(7, 7), None, PageSpan(7, 8), None, None, PageSpan(9, 9)]

    assert fill_between_agreeing_neighbours(spans) == [
        PageSpan(7, 7),
        PageSpan(7, 7),
        PageSpan(7, 8),
        None,
        None,
        PageSpan(9, 9),
    ]


def test_an_unplaced_chunk_at_either_end_stays_unplaced():
    assert fill_between_agreeing_neighbours([None, PageSpan(2, 2), None]) == [
        None,
        PageSpan(2, 2),
        None,
    ]


def test_parser_pages_win_and_the_text_layer_fills_the_rest():
    chunks = [
        "Acute otitis media is the most common bacterial infection in young children",
        "Scanned appendix listing antibiotic doses by body weight in kilograms",
    ]
    # The parser OCR'd page 1 only; the text layer has the appendix on page 3.
    parser_pages = [PAGES[0]]
    text_layer = [
        "",
        "",
        "Scanned appendix listing antibiotic doses by body weight in kilograms",
    ]

    assert assign_pages(chunks, parser_pages, text_layer) == [
        PageSpan(1, 1),
        PageSpan(3, 3),
    ]


def test_missing_sources_are_skipped():
    assert assign_pages(["some chunk text here"], None, []) == [None]


def test_words_drop_markdown_and_join_hyphenated_line_breaks():
    assert words("## **Otitis** _media_ in chil-\ndren") == [
        "otitis",
        "media",
        "in",
        "children",
    ]
