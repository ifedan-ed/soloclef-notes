"""How @-mentions and a notebook's document scope shape one knowledge-base search."""

from __future__ import annotations

import pytest

from app.agents.chat.multi_agent_chat.subagents.builtins.knowledge_base.tools.retrieval_plan import (
    plan_retrieval,
)

pytestmark = pytest.mark.unit


def test_a_plain_chat_searches_the_whole_knowledge_base():
    plan = plan_retrieval(
        document_types=None, mentioned_document_ids=[], document_scope=None
    )
    assert plan.scope is not None and plan.scope.document_ids is None


def test_mentions_confine_a_plain_chat():
    plan = plan_retrieval(
        document_types=None, mentioned_document_ids=[9, 2], document_scope=None
    )
    assert plan.scope.document_ids == (2, 9)


def test_a_notebook_chat_never_searches_outside_its_sources():
    plan = plan_retrieval(
        document_types=("FILE",),
        mentioned_document_ids=[],
        document_scope=frozenset({3, 1}),
    )
    assert plan.scope.document_ids == (1, 3)
    assert plan.scope.document_types == ("FILE",)


def test_mentions_inside_a_notebook_are_intersected_with_its_sources():
    plan = plan_retrieval(
        document_types=None,
        mentioned_document_ids=[1, 99],
        document_scope=frozenset({1, 2}),
    )
    assert plan.scope.document_ids == (1,)


def test_a_mention_outside_the_notebook_cannot_widen_it():
    plan = plan_retrieval(
        document_types=None,
        mentioned_document_ids=[99],
        document_scope=frozenset({1, 2}),
    )
    assert plan.scope.document_ids == (1, 2)


def test_a_notebook_without_sources_searches_nothing():
    plan = plan_retrieval(
        document_types=None, mentioned_document_ids=[5], document_scope=frozenset()
    )
    assert plan.scope is None
