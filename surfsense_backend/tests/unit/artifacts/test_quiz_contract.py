from __future__ import annotations

import json

import pytest

from app.artifacts.verification.formats.quiz import (
    QUIZ_MAX_QUESTIONS,
    QUIZ_MIN_QUESTIONS,
    check_quiz_json,
    parse_quiz,
    quiz_to_markdown,
)
from app.artifacts.verification.formats.registry import get_format_adapter

# Pure logic: run with the unit tests (CI's ``pytest -m unit``).
pytestmark = pytest.mark.unit


def _questions(count=QUIZ_MIN_QUESTIONS):
    return [
        {
            "question_text": f"Question {index}",
            "options": [f"A {index}", f"B {index}", f"C {index}", f"D {index}"],
            "correct_option_index": index % 4,
            "explanation_text": f"Explanation {index}",
        }
        for index in range(1, count + 1)
    ]


def _quiz(**overrides):
    value = {
        "schema_version": 1,
        "title": "HTTP fundamentals",
        "questions": _questions(),
    }
    value.update(overrides)
    return json.dumps(value, ensure_ascii=False).encode()


def test_parses_closed_quiz_and_projects_markdown():
    quiz = parse_quiz(_quiz())
    markdown = quiz_to_markdown(_quiz())

    assert len(quiz.questions) == QUIZ_MIN_QUESTIONS
    assert markdown.startswith("# HTTP fundamentals\n\n## Question 1")
    assert "### Correct answer" in markdown
    assert markdown.endswith("\n")
    assert check_quiz_json(_quiz()).clean


def test_answer_choices_are_a_list_so_each_shows_on_its_own_line():
    markdown = quiz_to_markdown(_quiz())

    # One line break alone joined A to D into a single paragraph.
    assert "### Options\n\n- A. A 1\n- B. B 1\n- C. C 1\n- D. D 1" in markdown


@pytest.mark.parametrize(
    ("data", "message"),
    [
        (b"", "empty"),
        (b"\xef\xbb\xbf{}", "byte-order mark"),
        (b'{"schema_version":1,"schema_version":1}', "duplicate JSON key"),
        (_quiz(schema_version=2), "schema_version"),
        (_quiz(extra=True), "Extra inputs"),
        (_quiz(questions=_questions(QUIZ_MIN_QUESTIONS - 1)), "between 5 and 30"),
        (_quiz(questions=_questions(QUIZ_MAX_QUESTIONS + 1)), "between 5 and 30"),
        (
            _quiz(
                questions=[
                    {
                        **_questions()[0],
                        "options": ["A", "B", "C"],
                    },
                    *_questions()[1:],
                ]
            ),
            "exactly four",
        ),
        (
            _quiz(
                questions=[
                    {
                        **_questions()[0],
                        "options": ["Same", " same ", "C", "D"],
                    },
                    *_questions()[1:],
                ]
            ),
            "distinct",
        ),
        (
            _quiz(
                questions=[
                    {**_questions()[0], "correct_option_index": 4},
                    *_questions()[1:],
                ]
            ),
            "less than or equal to 3",
        ),
        (
            _quiz(
                questions=[
                    {**_questions()[0], "question_text": r"Bad \(x"},
                    *_questions()[1:],
                ]
            ),
            "unclosed LaTeX",
        ),
    ],
)
def test_rejects_invalid_quizzes(data: bytes, message: str):
    result = check_quiz_json(data)
    assert not result.clean
    assert message in result.findings[0]


def test_quiz_adapter_is_programmatic_and_projection_backed():
    adapter = get_format_adapter("quiz")
    assert adapter.suffix == ".json"
    assert adapter.mime_type == "application/json"
    assert adapter.requires_visual_review is False
    assert adapter.markdown_projection is quiz_to_markdown


def _with_rationales(rationales):
    return _quiz(
        questions=[
            {**_questions()[0], "option_rationales": rationales},
            *_questions()[1:],
        ]
    )


def test_option_rationales_are_optional_and_projected_per_option():
    # Old quizzes (no rationales) are unchanged.
    assert "Why each option" not in quiz_to_markdown(_quiz())

    data = _with_rationales(["Wrong: a", "Right: b", r"Wrong: \(x^2\)", "Wrong: d"])
    quiz = parse_quiz(data)
    assert quiz.questions[0].option_rationales[1] == "Right: b"
    assert quiz.questions[1].option_rationales is None
    markdown = quiz_to_markdown(data)
    assert (
        "### Why each option\n\n- A. Wrong: a\n- B. Right: b\n- C. Wrong: \\(x^2\\)"
        in markdown
    )
    assert check_quiz_json(data).clean


@pytest.mark.parametrize(
    ("rationales", "message"),
    [
        (None, "omitted, not null"),
        (["a", "b", "c"], "exactly four"),
        (["a", "b", "c", "d", "e"], "exactly four"),
        (["a", "", "c", "d"], "option_rationales.1"),
        (["a", "b", r"bad \(x", "d"], "unclosed LaTeX"),
        (["a", "b", "c", "x" * 2001], "option_rationales.3"),
        ([1, "b", "c", "d"], "option_rationales"),
    ],
)
def test_rejects_invalid_option_rationales(rationales, message):
    result = check_quiz_json(_with_rationales(rationales))
    assert not result.clean
    assert message in result.findings[0]
