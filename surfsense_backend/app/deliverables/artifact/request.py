"""The persisted request of a queued ``artifact`` deliverable job."""

from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field

from .item_count import ItemCount
from .required_fragment import RequiredFragment

# Every sandbox-skill output the studio can start without a chat turn.
ArtifactOutput = Literal[
    "report", "slides", "mindmap", "flashcards", "quiz", "data_table", "infographic"
]

MAX_PROMPT_CHARS = 120_000


class ArtifactJobRequestV1(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    version: Literal[1]
    output: ArtifactOutput
    prompt: Annotated[str, Field(min_length=1, max_length=MAX_PROMPT_CHARS)]
    root_thread_id: Annotated[int, Field(gt=0)]
    # The style the requester already chose, so the run never asks for one.
    infographic_style: Annotated[str, Field(max_length=64)] | None = None
    # The same dialog's canvas shape, level of detail and own words.
    infographic_orientation: Literal["landscape", "portrait", "square"] | None = None
    infographic_detail: Literal["concise", "standard", "detailed"] | None = None
    infographic_description: Annotated[str, Field(max_length=2000)] | None = None
    # Prepared HTML the saved page must contain (see required_fragment).
    required_fragment: RequiredFragment | None = None
    # How many cards or questions a flashcards or quiz run must save.
    item_count: ItemCount | None = None
