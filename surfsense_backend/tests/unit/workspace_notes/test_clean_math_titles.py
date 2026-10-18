"""The one-off that writes note titles holding an answer's TeX as words."""

from __future__ import annotations

import pytest

from app.workspace_notes.clean_math_titles import new_title_for, title_from_answer

pytestmark = pytest.mark.unit

# The answer and the title from the owner's screenshot (2026-10-10).
PEARSON = (
    "The correlation was moderate and positive: Pearson's \\(r = 0.463\\), statistically "
    "significant (\\(p < 0.001\\)) [citation:12].\n\nMore."
)
OLD_TITLE = (
    "The correlation was moderate and positive: Pearson's \\(r = 0.463\\), statistical…"
)


def test_save_to_note_names_an_answer_as_the_web_does_today():
    assert title_from_answer(PEARSON) == (
        "The correlation was moderate and positive: Pearson's r = 0.463, statistically s…"
    )
    assert title_from_answer(
        "## Iron deficiency\n\nFerritin is low [citation:12]."
    ) == ("Iron deficiency")


def test_a_kept_answer_still_titled_as_saved_is_named_again_from_its_text():
    assert new_title_for(OLD_TITLE, origin="chat", content=PEARSON) == (
        "The correlation was moderate and positive: Pearson's r = 0.463, statistically s…",
        "text",
    )


def test_a_renamed_or_edited_note_keeps_its_own_title_as_words():
    # Its text no longer starts with its title (edited, or renamed).
    assert new_title_for(OLD_TITLE, origin="chat", content="Something else.") == (
        "The correlation was moderate and positive: Pearson's r = 0.463, statistical…",
        "title",
    )
    # A note written by hand is never named from its text.
    assert new_title_for(
        "Effect \\(d\\)", origin="hand", content="Effect d is big"
    ) == (
        "Effect d",
        "title",
    )


def test_money_is_left_alone():
    assert new_title_for("Budget $3 and $5", origin="hand", content="") == (
        "Budget $3 and $5",
        "title",
    )
