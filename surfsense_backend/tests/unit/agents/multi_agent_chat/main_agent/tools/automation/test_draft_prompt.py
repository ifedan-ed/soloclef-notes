"""The automation drafter keeps results as notes in notebooks, never folders."""

from __future__ import annotations

import pytest

from app.agents.chat.multi_agent_chat.main_agent.tools.automation.notebook_choices import (
    NotebookChoice,
    NotebookChoices,
    chat_thread_id,
)
from app.agents.chat.multi_agent_chat.main_agent.tools.automation.prompt import (
    build_draft_prompt,
)

pytestmark = pytest.mark.unit


def test_the_drafter_is_given_the_notebooks_and_this_chats_own():
    choices = NotebookChoices(
        notebooks=[
            NotebookChoice(id=18, title="Email Search Assistance"),
            NotebookChoice(id=11, title="IQ and motivation"),
        ],
        current=NotebookChoice(id=18, title="Email Search Assistance"),
    )
    prompt = build_draft_prompt(
        workspace_id=1, intent="Daily unread email summary as a note", notebooks=choices
    )
    assert '- id=18 "Email Search Assistance"' in prompt
    assert '- id=11 "IQ and motivation"' in prompt
    assert (
        'This chat belongs to the notebook id=18 "Email Search Assistance".' in prompt
    )
    assert "if they name none, use this chat's notebook" in prompt


def test_no_example_saves_a_note_into_a_folder():
    prompt = build_draft_prompt(workspace_id=1, intent="x")
    assert "note in folder" not in prompt
    assert "summary_folder_id" not in prompt
    assert '"save_to_notebook_id": 31' in prompt
    assert "Notes live in\n  notebooks, never in folders" in prompt


def test_without_notebooks_the_drafter_leaves_the_setting_out():
    prompt = build_draft_prompt(workspace_id=1, intent="x", notebooks=None)
    assert "- (none)" in prompt
    assert "leave save_to_notebook_id out" in prompt.replace("\n", " ")


@pytest.mark.parametrize(
    ("config", "thread_id"),
    [
        ({"configurable": {"thread_id": 42}}, 42),
        ({"configurable": {"thread_id": "42"}}, 42),
        ({"configurable": {"thread_id": "6f1c-uuid"}}, None),
        ({"configurable": {"thread_id": True}}, None),
        ({}, None),
        (None, None),
    ],
)
def test_the_chat_thread_is_read_off_the_tool_call_config(config, thread_id):
    assert chat_thread_id(config) == thread_id
