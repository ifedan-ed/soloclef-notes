"""A study deck is saved at the size the request asked for."""

from __future__ import annotations

import json

import pytest

from app.artifacts.verification.formats.flashcards import (
    FLASHCARDS_MIN_CARDS,
    check_flashcards_json,
    flashcards_to_markdown,
)
from app.artifacts.verification.formats.quiz import check_quiz_json
from app.deliverables.artifact.item_count import (
    ItemCount,
    ItemCountError,
    applies_to,
    enforce_item_count,
)

pytestmark = pytest.mark.unit


def _deck(n: int) -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "title": "Iron",
            "cards": [
                {"front_text": f"Q{i}", "back_text": f"A{i}"} for i in range(1, n + 1)
            ],
        }
    ).encode()


def _quiz(n: int) -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "title": "Iron",
            "questions": [
                {
                    "question_text": f"Question {i}?",
                    "options": [f"{i}{c}" for c in "abcd"],
                    "correct_option_index": 0,
                    "explanation_text": "Because.",
                }
                for i in range(1, n + 1)
            ],
        }
    ).encode()


def test_a_ten_card_deck_now_verifies():
    assert FLASHCARDS_MIN_CARDS <= 10
    assert check_flashcards_json(_deck(10)).clean


def test_an_oversized_deck_is_trimmed_to_the_target_keeping_order():
    trimmed = enforce_item_count(
        "flashcards", _deck(18), ItemCount(target=10, minimum=9)
    )

    assert check_flashcards_json(trimmed).clean
    cards = json.loads(trimmed)["cards"]
    assert [c["front_text"] for c in cards] == [f"Q{i}" for i in range(1, 11)]
    assert flashcards_to_markdown(trimmed).count("## Card ") == 10


def test_a_deck_within_the_range_is_returned_byte_for_byte():
    data = _deck(9)
    assert (
        enforce_item_count("flashcards", data, ItemCount(target=10, minimum=9)) is data
    )


def test_too_few_is_refused_with_the_numbers():
    with pytest.raises(
        ItemCountError, match=r"has 6 cards; .* asked for 10 \(at least 9\)"
    ):
        enforce_item_count("flashcards", _deck(6), ItemCount(target=10, minimum=9))


def test_a_quiz_is_trimmed_too():
    trimmed = enforce_item_count("quiz", _quiz(12), ItemCount(target=10, minimum=9))
    assert check_quiz_json(trimmed).clean
    assert len(json.loads(trimmed)["questions"]) == 10


def test_only_study_formats_are_counted():
    assert applies_to("flashcards") and applies_to("quiz")
    assert not applies_to("mindmap")


def test_the_minimum_cannot_exceed_the_target():
    with pytest.raises(ValueError):
        ItemCount(target=5, minimum=6)
