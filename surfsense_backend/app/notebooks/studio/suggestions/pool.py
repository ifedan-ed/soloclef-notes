"""The cached pool per dialog, and the three ideas shown from it at a time.

The pool lives on the notebook, keyed by the enabled source set: a changed
source list drops every pool at once. Within one source set there is a pool
per output type, format and language, so switching a podcast from "Deep
dive" to "Debate" asks for debate questions rather than reusing dives.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field

from sqlalchemy.orm.attributes import flag_modified

from ...persistence import Notebook
from .schemas import WINDOW


@dataclass(frozen=True)
class SuggestionPool:
    ideas: list[str] = field(default_factory=list)
    examples: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class StudioSuggestions:
    """One window of ideas to show now, and the pool it came from."""

    suggestions: list[str] = field(default_factory=list)
    placeholder: str | None = None
    pool: list[str] = field(default_factory=list)
    examples: list[str] = field(default_factory=list)


def suggestions_key(document_ids: list[int]) -> str:
    return hashlib.sha256(
        ",".join(str(i) for i in sorted(document_ids)).encode()
    ).hexdigest()


def pool_key(kind: str, variant: str | None, language: str | None) -> str:
    return f"{kind}|{variant or ''}|{language or ''}"


def window(pool: SuggestionPool, offset: int = 0) -> StudioSuggestions:
    """The ``offset``-th group of three, wrapping round the pool.

    Consecutive positions modulo the pool size are always distinct, so any
    pool of three or more yields three different ideas for every offset.
    """
    size = len(pool.ideas)
    shown = (
        [pool.ideas[(offset * WINDOW + i) % size] for i in range(WINDOW)]
        if size >= WINDOW
        else list(pool.ideas)
    )
    placeholder = pool.examples[offset % len(pool.examples)] if pool.examples else None
    return StudioSuggestions(
        suggestions=shown,
        placeholder=placeholder,
        pool=list(pool.ideas),
        examples=list(pool.examples),
    )


def cached_pool(notebook: Notebook, sources: str, key: str) -> SuggestionPool | None:
    if notebook.focus_suggestions_key != sources:
        return None
    cache = notebook.focus_suggestions
    entry = cache.get(key) if isinstance(cache, dict) else None
    # Entries from before pools ({"suggestions", "placeholder"}) are regenerated.
    if not isinstance(entry, dict) or not isinstance(entry.get("ideas"), list):
        return None
    ideas = [i for i in entry["ideas"] if isinstance(i, str) and i]
    if len(ideas) < WINDOW:
        return None
    examples = [e for e in entry.get("examples") or [] if isinstance(e, str) and e]
    return SuggestionPool(ideas=ideas, examples=examples)


def store_pool(
    notebook: Notebook, sources: str, key: str, pool: SuggestionPool
) -> None:
    same_sources = notebook.focus_suggestions_key == sources and isinstance(
        notebook.focus_suggestions, dict
    )
    cache = dict(notebook.focus_suggestions) if same_sources else {}
    cache[key] = {"ideas": pool.ideas, "examples": pool.examples}
    notebook.focus_suggestions = cache
    notebook.focus_suggestions_key = sources
    flag_modified(notebook, "focus_suggestions")
