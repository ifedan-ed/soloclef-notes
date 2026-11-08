"""How much retrieved text one search may hand the prompt.

The allowance is counted in characters. A chunk count cannot say "the same
amount of text": re-size the chunks and the same count is a different prompt,
which is what kept chunk sizing frozen (``custom/README.md``). Characters are
also what can be counted on the search path -- a token count would be truer to
what the model pays, but only by loading a tokenizer into every search.

Two allowances, both explicit. The search allowance is the whole prompt's
share of retrieved text; the document allowance is the most any one document
may take of it, so that a chunk-dense document cannot answer every question
alone. The second is not redundant: ranked selection spends the budget on the
best chunks wherever they are, and the best chunks are often all in one place.

The caller offers candidates in priority order -- for a fused search, best
rank first -- and gets back the keys that fit. A candidate too large for what
is left is passed over rather than ending the fill, so one long chunk does not
cost the shorter ones ranked behind it.
"""

from __future__ import annotations

from collections.abc import Hashable, Iterable
from dataclasses import dataclass


@dataclass(frozen=True)
class ContextBudget:
    """Characters of retrieved text one search, and one document, may spend."""

    search_chars: int
    document_chars: int


@dataclass(frozen=True)
class BudgetCandidate:
    """One chunk offered to the budget, identified by whatever the caller keys on."""

    key: Hashable
    document_id: int
    chars: int


def admit_within_budget(
    candidates: Iterable[BudgetCandidate], budget: ContextBudget
) -> set[Hashable]:
    """The keys that fit, taking ``candidates`` in the order they arrive."""
    admitted: set[Hashable] = set()
    spent = 0
    spent_by_document: dict[int, int] = {}
    for candidate in candidates:
        on_document = spent_by_document.get(candidate.document_id, 0)
        if spent + candidate.chars > budget.search_chars:
            continue
        if on_document + candidate.chars > budget.document_chars:
            continue
        admitted.add(candidate.key)
        spent += candidate.chars
        spent_by_document[candidate.document_id] = on_document + candidate.chars
    return admitted


def configured_context_budget() -> ContextBudget:
    """This deployment's allowances, refused rather than guessed if unusable."""
    from app.config import config

    search_chars = int(config.RETRIEVAL_CONTEXT_BUDGET_CHARS)
    document_chars = int(config.RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS)
    if search_chars < 1:
        raise ValueError(
            f"RETRIEVAL_CONTEXT_BUDGET_CHARS={search_chars} must be at least 1"
        )
    if document_chars < 1:
        raise ValueError(
            f"RETRIEVAL_DOCUMENT_CONTEXT_BUDGET_CHARS={document_chars} "
            "must be at least 1"
        )
    return ContextBudget(search_chars=search_chars, document_chars=document_chars)


__all__ = [
    "BudgetCandidate",
    "ContextBudget",
    "admit_within_budget",
    "configured_context_budget",
]
