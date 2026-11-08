"""What an automation step may read, and what it is told about it."""

from __future__ import annotations

import pytest

from app.agents.chat.retrieval_scope import RetrievalScope
from app.automations.actions.builtin.agent_task.invoke import (
    automation_brief,
    resolve_retrieval_scope,
)
from app.automations.actions.builtin.agent_task.params import AgentTaskActionParams

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("requested", "connector", "documents", "expected"),
    [
        (None, False, False, RetrievalScope.DOCUMENTS),
        ("documents", False, False, RetrievalScope.DOCUMENTS),
        ("web", False, False, RetrievalScope.WEB),
        ("all", False, False, RetrievalScope.ALL),
        # An @-mentioned connected account (Browserbase) needs the web side.
        (None, True, False, RetrievalScope.ALL),
        ("documents", True, False, RetrievalScope.ALL),
        # A web step that names documents or a notebook can still read them.
        ("web", False, True, RetrievalScope.ALL),
        ("web", True, False, RetrievalScope.WEB),
    ],
)
def test_resolve_retrieval_scope(requested, connector, documents, expected):
    assert (
        resolve_retrieval_scope(
            requested, mentions_connector=connector, needs_documents=documents
        )
        is expected
    )


def test_the_brief_says_the_run_is_unattended_and_how_to_find_notebooks():
    brief = automation_brief(RetrievalScope.DOCUMENTS)
    assert "unattended" in brief
    assert "list_notebooks" in brief
    assert "Studio" in brief
    assert "report_outcome" in brief
    assert "'Can use'" in brief  # how to fix a documents-only step


def test_the_brief_for_a_web_step_names_the_web_tools():
    brief = automation_brief(RetrievalScope.ALL, notebook_title="Grant ideas")
    assert "public web" in brief
    assert "'Grant ideas'" in brief


def test_old_definitions_without_the_new_params_still_validate():
    params = AgentTaskActionParams.model_validate({"query": "hi"})
    assert params.retrieval_scope is None and params.notebook_id is None
    with pytest.raises(ValueError):
        AgentTaskActionParams.model_validate({"query": "hi", "retrieval_scope": "x"})


def test_a_step_that_saves_to_a_notebook_is_told_not_to_write_a_file():
    brief = automation_brief(RetrievalScope.ALL, save_to_notebook_title="Email digests")
    assert "saved automatically as a note in the notebook 'Email digests'" in brief
    assert "do not write it to a file or folder" in brief


def test_a_step_that_saves_nowhere_is_not_told_about_notes():
    assert "saved automatically" not in automation_brief(RetrievalScope.ALL)
