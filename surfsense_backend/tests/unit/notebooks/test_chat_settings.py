"""What "Configure chat" and follow-up material turn into."""

from __future__ import annotations

import pytest

from app.notebooks.chat.context_block import render_notebook_context
from app.notebooks.chat.followups import (
    MAX_ANSWER_CHARS,
    followup_material,
    parse_questions,
)
from app.notebooks.chat.pins import NotebookPins
from app.notebooks.chat.settings import ChatSettings, render_chat_settings

pytestmark = pytest.mark.unit


def test_defaults_add_nothing():
    assert render_chat_settings(ChatSettings()) == []


def test_learning_guide_and_length():
    lines = render_chat_settings(ChatSettings(goal="learning_guide", length="longer"))
    assert "learning guide" in lines[0]
    assert "longer" in lines[1]


def test_custom_goal_uses_the_instructions_and_ignores_blank_ones():
    lines = render_chat_settings(
        ChatSettings(goal="custom", instructions="  Be   terse. ")
    )
    assert lines[0].endswith("Be terse.")
    assert render_chat_settings(ChatSettings(goal="custom", instructions="  ")) == []


def test_context_block_with_sources_keeps_settings_inside_the_block():
    block = render_notebook_context(
        NotebookPins(
            notebook_id=1,
            title="QI",
            document_ids=[3],
            source_titles=["Paper"],
            settings=ChatSettings(length="shorter"),
        )
    )
    assert block.startswith("<notebook_context>")
    assert block.endswith("</notebook_context>")
    assert "- Paper" in block and "short, direct answers" in block
    assert "no enabled sources" not in block
    # Sources only: the web is never part of answering.
    assert "do not search the web or offer to" in block
    assert "retrieval scope" not in block


def test_empty_notebook_asks_for_next_steps():
    block = render_notebook_context(NotebookPins(notebook_id=1, title="QI"))
    assert "no enabled sources yet" in block
    # The research box is the way to bring web pages in.
    assert "Search the web for new sources" in block


def test_followup_material_is_bounded():
    material = followup_material("x" * (MAX_ANSWER_CHARS + 500), ["A", "B"])
    assert material.count("x") == MAX_ANSWER_CHARS
    assert "- A" in material


def test_followups_accept_object_and_array_replies():
    assert parse_questions(
        'Here: {"suggestions": ["One?", "Two?", "Three?", "Four?"]}'
    ) == ["One?", "Two?", "Three?"]
    assert parse_questions('["A", "B"]') == ["A", "B"]
    assert parse_questions("nothing") == []
