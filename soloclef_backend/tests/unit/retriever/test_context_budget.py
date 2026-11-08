"""A search's context is bounded in characters, and the best-ranked text wins.

A chunk count cannot express "the same amount of text": the prompt pays for
characters, so the allowance is counted in them. These tests pin the
arithmetic the retrievers share -- what fits, what is passed over, and what a
single document may take while other documents still have a claim.
"""

from __future__ import annotations

import pytest

from app.retriever.context_budget import (
    BudgetCandidate,
    ContextBudget,
    admit_within_budget,
)

pytestmark = pytest.mark.unit


def _candidates(*sizes_by_document: tuple[int, int]) -> list[BudgetCandidate]:
    """Candidates in priority order: ``(document_id, chars)`` best first."""
    return [
        BudgetCandidate(key=index, document_id=document_id, chars=chars)
        for index, (document_id, chars) in enumerate(sizes_by_document)
    ]


def test_nothing_offered_admits_nothing():
    budget = ContextBudget(search_chars=26_000, document_chars=16_000)

    assert admit_within_budget([], budget) == set()


def test_one_document_may_fill_the_search_up_to_its_own_allowance():
    """A single document answering everything is still bounded by its own share."""
    budget = ContextBudget(search_chars=1_000, document_chars=600)
    candidates = _candidates((1, 300), (1, 300), (1, 300))

    admitted = admit_within_budget(candidates, budget)

    assert admitted == {0, 1}


def test_a_document_with_more_chunks_than_fit_keeps_the_best_ranked():
    budget = ContextBudget(search_chars=1_000, document_chars=1_000)
    candidates = _candidates((1, 400), (1, 400), (1, 400))

    assert admit_within_budget(candidates, budget) == {0, 1}


def test_a_chunk_too_large_for_what_is_left_is_passed_over_not_a_full_stop():
    """A long chunk must not cost the shorter, lower-ranked ones behind it."""
    budget = ContextBudget(search_chars=1_000, document_chars=1_000)
    candidates = _candidates((1, 900), (1, 500), (1, 100))

    assert admit_within_budget(candidates, budget) == {0, 2}


def test_what_one_document_cannot_take_is_left_for_the_others():
    budget = ContextBudget(search_chars=1_200, document_chars=400)
    candidates = _candidates((1, 400), (1, 400), (2, 400), (3, 400))

    assert admit_within_budget(candidates, budget) == {0, 2, 3}


def test_a_chunk_larger_than_the_document_allowance_is_never_admitted():
    budget = ContextBudget(search_chars=10_000, document_chars=500)
    candidates = _candidates(
        (1, 600),
    )

    assert admit_within_budget(candidates, budget) == set()


@pytest.mark.parametrize(
    ("search_chars", "document_chars"),
    [(0, 16_000), (26_000, 0), (-1, 16_000), (26_000, -1)],
)
def test_an_allowance_below_one_character_is_refused(
    search_chars, document_chars, monkeypatch
):
    """A misconfigured budget fails the search rather than emptying the prompt."""
    from app.config import config
    from app.retriever.context_budget import configured_context_budget

    monkeypatch.setattr(config, "RETRIEVAL_CONTEXT_BUDGET_CHARS", search_chars)
    monkeypatch.setattr(
        config, "RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS", document_chars
    )

    with pytest.raises(ValueError):
        configured_context_budget()


def test_the_configured_budget_is_what_the_settings_say():
    from app.config import config
    from app.retriever.context_budget import configured_context_budget

    budget = configured_context_budget()

    assert budget.search_chars == int(config.RETRIEVAL_CONTEXT_BUDGET_CHARS)
    assert budget.document_chars == int(config.RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS)
