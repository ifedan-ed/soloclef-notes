"""The report view's state, kept in the report artifact's metadata.

One entry under ``metadata["report_view"]``, for the artifact generation it
was made for: a revised report (a new generation) gets new suggestions, and
its old dismissals and feedback do not carry over. Pure functions over the
metadata dict, so the rules are tested without a database.

    {"generation": 3, "language": "en", "ready": true,
     "suggestions": [{"id": "g3s1", "section": 0, "heading": "...",
                      "kind": "quiz", "title": "...", "focus": "...",
                      "state": "open" | "added" | "dismissed",
                      "output_id": null, "attempts": 0}],
     "feedback": null | "good" | "bad"}

``ready`` is set once suggestions were generated (even none). ``feedback`` is
the retired Good/Bad rating: nothing sets it any more, and a stored one is
carried over unchanged when suggestions are made.
"""

from __future__ import annotations

import copy
from typing import Any

from .schemas import ReportSuggestion
from .sections import ReportOutline

KEY = "report_view"
STATES = ("open", "added", "dismissed")


def entry(metadata: Any, generation: int) -> dict | None:
    """The stored entry for this generation, if any."""
    value = metadata.get(KEY) if isinstance(metadata, dict) else None
    if not isinstance(value, dict) or value.get("generation") != generation:
        return None
    if not isinstance(value.get("suggestions"), list):
        value = {**value, "suggestions": [], "ready": False}
    return value


def empty_entry(generation: int) -> dict:
    return {
        "generation": generation,
        "suggestions": [],
        "ready": False,
        "feedback": None,
    }


def suggestions_entry(
    generation: int,
    language: str,
    outline: ReportOutline,
    suggestions: list[ReportSuggestion],
    feedback: str | None = None,
) -> dict:
    items = []
    for number, suggestion in enumerate(
        sorted(suggestions, key=lambda s: s.section), start=1
    ):
        section = outline.sections[suggestion.section - 1]
        items.append(
            {
                "id": f"g{generation}s{number}",
                "section": section.index,
                "heading": section.heading,
                "kind": suggestion.kind,
                "title": suggestion.title,
                "focus": suggestion.focus,
                "state": "open",
                "output_id": None,
                "attempts": 0,
            }
        )
    return {
        "generation": generation,
        "language": language,
        "ready": True,
        "suggestions": items,
        "feedback": feedback,
    }


def with_entry(metadata: Any, value: dict) -> dict:
    """A copy of ``metadata`` holding ``value`` as the report view's entry."""
    updated = dict(metadata) if isinstance(metadata, dict) else {}
    updated[KEY] = value
    return updated


def find(value: dict, suggestion_id: str) -> dict | None:
    return next((s for s in value["suggestions"] if s.get("id") == suggestion_id), None)


def changed(value: dict, suggestion_id: str, **changes: Any) -> dict:
    """A copy of the entry with one suggestion changed; KeyError if unknown."""
    updated = copy.deepcopy(value)
    item = find(updated, suggestion_id)
    if item is None:
        raise KeyError(suggestion_id)
    item.update(changes)
    return updated
