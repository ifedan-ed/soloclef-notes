# ruff: noqa: RUF001  (Greek letters and maths signs are the point)
"""Maths and Markdown in a note's title and preview, as the words they show.

The same cases as the web's tests/unit/notes/note-title-math.test.ts: the
backend and the web must name a note alike.
"""

from __future__ import annotations

import time

import pytest

from app.notebooks.schemas import NoteCreate
from app.workspace_notes.note_documents import preview_of
from app.workspace_notes.readable_text import (
    MATH_TEXT_MAX_CHARS,
    first_line_of_words,
    math_as_text,
    plain_text,
    readable_title,
    tex_as_text,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("tex", "words"),
    [
        ("r = 0.463", "r = 0.463"),
        ("p < 0.001", "p < 0.001"),
        ("\\alpha \\le 0.05", "α ≤ 0.05"),
        ("\\mu \\pm 2\\sigma", "μ ± 2σ"),
        ("x^2 + y^{-1}", "x² + y⁻¹"),
        ("e^{i\\pi}", "e^(iπ)"),
        ("\\beta_1, x_{max}, p_{adj}", "β₁, xₘₐₓ, p_adj"),
        ("\\frac{1}{2}", "1/2"),
        ("\\frac{a+b}{n - 1}", "(a+b)/(n - 1)"),
        ("\\sqrt{2}, \\sqrt{n+1}, \\sqrt[3]{8}", "√2, √(n+1), ∛8"),
        ("\\bar{x} \\hat{\\beta}", "x̄ β̂"),
        ("37^\\circ\\text{C}", "37°C"),
        ("\\mathrm{OR} = 1.8\\,\\text{(95\\% CI)}", "OR = 1.8 (95% CI)"),
        ("\\left( \\frac{a}{b} \\right)", "( a/b )"),
        ("\\mathcal{L}(\\theta)", "L(θ)"),
        ("\\begin{pmatrix} a & b \\end{pmatrix}", "a b"),
        ("\\unknown{x}", "x"),
        ("\\frac{1}", "1/"),
        ("\\constructor", ""),
    ],
)
def test_tex_becomes_unicode_where_it_can_and_letters_where_it_cannot(tex, words):
    assert tex_as_text(tex) == words


@pytest.mark.parametrize(
    ("text", "words"),
    [
        ("a $$x^2$$ b", "a x² b"),
        ("a \\[x\\] b \\(y\\) c", "a x b y c"),
        ("\\begin{equation}E = mc^2\\end{equation}", "E = mc²"),
        ("where $\\alpha$ is", "where α is"),
        # Money is not maths.
        ("from $5 to $10", "from $5 to $10"),
        ("between $5-$10 or $ 3 $", "between $5-$10 or $ 3 $"),
        # A delimiter whose pair was cut off is dropped.
        ("Pearson's \\(r = 0.4…", "Pearson's r = 0.4…"),
    ],
)
def test_every_form_of_maths_is_found(text, words):
    assert math_as_text(text) == words


def test_what_runs_outside_the_maths_does_not_reach_into_it():
    assert math_as_text("_a_ \\(x_1\\)", lambda text: text.replace("_", "")) == "a x₁"


def test_markdown_reads_as_its_words():
    assert (
        plain_text("> **Odds** of $\\beta_1$ [here](https://x.org)")
        == "Odds of β₁ here"
    )
    assert plain_text("p > 0.05 > q") == "p > 0.05 > q"
    assert plain_text("- First \\(a\\) - then \\(b\\)") == "First a - then b"
    assert plain_text("It costs $3 and $5, not \\$4.") == "It costs $3 and $5, not $4."
    assert (
        first_line_of_words("\n\n## Result: $$R^2 = 0.21$$\nmore")
        == "Result: R² = 0.21"
    )
    assert first_line_of_words("\\[\n\\chi^2 = 4.2\n\\]\n\nSo it is.") == "χ² = 4.2"
    assert first_line_of_words("  \n ") == ""


def test_a_title_keeps_everything_but_its_maths():
    old = "The correlation was moderate and positive: Pearson's \\(r = 0.463\\), statistical…"
    assert readable_title(old) == (
        "The correlation was moderate and positive: Pearson's r = 0.463, statistical…"
    )
    assert readable_title("p_adj **bold** $3 and $5") == "p_adj **bold** $3 and $5"


def test_a_note_preview_shows_its_maths_as_text():
    assert preview_of("# Result\n\nPearson's \\(r = 0.463\\), **strong**") == (
        "Result Pearson's r = 0.463, strong"
    )


def test_an_answer_saved_as_a_note_is_titled_in_words_whatever_sent_it():
    # A phone on an older update sends the raw first line.
    raw = "Pearson's \\(r = 0.463\\), statistical…"
    assert (
        NoteCreate(title=raw, origin="chat").title
        == "Pearson's r = 0.463, statistical…"
    )
    # A title typed by hand is kept as typed.
    assert NoteCreate(title=raw, origin="hand").title == raw


# Opening delimiters with no closing one: the maths pattern scanned from each
# to the end, so the time grew with the square of the length (the first body,
# 60,000 characters, took 22 s before the cap; a megabyte, hours) on the event
# loop of every note save.
_CRAFTED = {
    "display brackets": "\\[" * 30_000,
    "inline parens": "\\(a" * 20_000,
    "environments": "\\begin{equation}" * 4_000,
}


@pytest.mark.parametrize("body", list(_CRAFTED.values()), ids=list(_CRAFTED))
def test_a_crafted_note_body_is_titled_in_bounded_time(body):
    started = time.perf_counter()
    first_line_of_words(body)
    preview_of(body)
    assert time.perf_counter() - started < 1.0


def test_only_the_opening_of_a_long_note_is_read():
    opening = "Results \\(r = 0.463\\)\n\n"
    body = opening + "x" * (MATH_TEXT_MAX_CHARS * 50) + " $\\alpha$"
    assert first_line_of_words(body) == "Results r = 0.463"
    assert len(math_as_text(body)) <= MATH_TEXT_MAX_CHARS
    # Past the cap, nothing is converted (or kept).
    assert "α" not in math_as_text(body)
    assert plain_text("**Words** and $x^2$") == "Words and x²"


# A link or image longer than the cap at the top of a note: the cut fell
# inside it, the pattern no longer matched, and the title was the markup.
_DATA_IMAGE = "data:image/png;base64," + "iVBORw0KGgo" * 400  # 4,422 characters


def test_a_note_opening_with_a_long_image_is_titled_by_its_words():
    assert len(_DATA_IMAGE) > MATH_TEXT_MAX_CHARS
    assert first_line_of_words(f"![]({_DATA_IMAGE})\n\nWard round notes") == (
        "Ward round notes"
    )
    assert first_line_of_words(f"![Chest X-ray]({_DATA_IMAGE})\nmore") == "Chest X-ray"
    assert preview_of(f"![]({_DATA_IMAGE})\n\nWard round notes") == "Ward round notes"


def test_a_note_opening_with_a_long_link_is_titled_by_its_words():
    url = "https://example.org/" + "a" * (MATH_TEXT_MAX_CHARS * 2)
    assert first_line_of_words(f"[The guideline]({url}) says") == "The guideline says"
    label = "words " * 500
    assert first_line_of_words(f"[{label}](https://x.org)").startswith("words words")
    assert "](" not in first_line_of_words(f"[{label}](https://x.org)")


def test_links_are_found_in_time_and_maths_in_brackets_stays_maths():
    started = time.perf_counter()
    for unit in ("[", "![", "](", "[a](", "[a]"):
        first_line_of_words(unit * 100_000)
        preview_of(unit * 100_000)
    assert time.perf_counter() - started < 1.0
    assert first_line_of_words("\\[x^2\\](1) holds") == "x²(1) holds"
