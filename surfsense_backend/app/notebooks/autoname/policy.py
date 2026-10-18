"""When a notebook may name itself, and from what.

- Only while ``title_source`` is ``auto``: the first rename by the user (header,
  customize dialog, mobile app) sets it to ``user`` and naming stops for good.
- The first naming happens when the first source is ready (from the sources),
  or, with no ready source yet, when the first question is asked (from it).
- One refinement: when more sources are ready than the last naming used, within
  ``REFINE_WINDOW`` of the first naming, the notebook is renamed from all its
  ready sources. That covers a batch upload whose files finish one by one, and a
  notebook named from its question whose first source lands soon after.
- Never more than ``MAX_RUNS`` automatic names in total.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Literal

MAX_RUNS = 2
REFINE_WINDOW = timedelta(minutes=30)

Trigger = Literal["source", "question", "backfill"]
Basis = Literal["sources", "question"]


@dataclass(frozen=True)
class AutoTitleState:
    title_source: str
    runs: int = 0
    at: datetime | None = None
    # Ready sources the last naming used; 0 means it came from a question.
    sources: int = 0

    @classmethod
    def of(cls, title_source: str | None, auto_title: dict | None) -> AutoTitleState:
        data = auto_title if isinstance(auto_title, dict) else {}
        at = None
        raw_at = data.get("at")
        if isinstance(raw_at, str):
            try:
                at = datetime.fromisoformat(raw_at)
            except ValueError:
                at = None
        return cls(
            title_source=title_source or "user",
            runs=_int(data.get("runs")),
            at=at,
            sources=_int(data.get("sources")),
        )


def _int(value: object) -> int:
    return value if isinstance(value, int) and value >= 0 else 0


def decide(
    state: AutoTitleState,
    *,
    ready_sources: int,
    has_question: bool,
    trigger: Trigger,
    now: datetime | None = None,
) -> Basis | None:
    """What to name the notebook from now, or None to leave it alone."""
    if state.title_source != "auto" or state.runs >= MAX_RUNS:
        return None
    if state.runs == 0:
        if ready_sources > 0:
            return "sources"
        return "question" if has_question else None
    # Already named once: only newly ready sources refine it, and only soon after.
    if trigger != "source" or ready_sources <= state.sources:
        return None
    now = now or datetime.now(UTC)
    if state.at is None or now - state.at > REFINE_WINDOW:
        return None
    return "sources"


def next_state(runs: int, *, basis: Basis, ready_sources: int, now: datetime) -> dict:
    return {
        "runs": runs + 1,
        "at": now.isoformat(),
        "sources": ready_sources if basis == "sources" else 0,
        "basis": basis,
    }
