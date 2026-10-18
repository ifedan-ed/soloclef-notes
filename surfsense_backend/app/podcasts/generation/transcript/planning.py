"""Internal shapes the transcript graph passes between its nodes.

These are generation-time artifacts (the outline and per-segment drafts), not
persisted or API-facing. Segment drafts reuse :class:`TranscriptTurn` so the
speaker-slot contract and turn validation are identical to the final transcript.
"""

from __future__ import annotations

from typing import Any

from pydantic import BaseModel, Field, field_validator

from app.podcasts.schemas import TranscriptTurn


class OutlineSegment(BaseModel):
    """One planned beat of the conversation, drafted independently."""

    title: str = Field(..., min_length=1)
    talking_points: list[str] = Field(default_factory=list)
    target_words: int = Field(..., ge=1)

    @field_validator("target_words", mode="before")
    @classmethod
    def _at_least_one_word(cls, value: Any) -> Any:
        """A zero or negative size is a planning slip, not a reason to refuse
        the whole outline; the drafter treats it as "as short as possible"."""
        if isinstance(value, int | float) and not isinstance(value, bool):
            return max(1, int(value))
        return value


class Outline(BaseModel):
    """The full plan: ordered segments sized to the target duration."""

    segments: list[OutlineSegment] = Field(..., min_length=1)


class SegmentDraft(BaseModel):
    """The dialogue a single segment produced."""

    turns: list[TranscriptTurn] = Field(default_factory=list)
