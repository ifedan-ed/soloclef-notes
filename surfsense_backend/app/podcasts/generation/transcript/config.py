"""Configurable inputs for the transcript-drafting graph."""

from __future__ import annotations

from dataclasses import dataclass, fields
from typing import Any

from langchain_core.runnables import RunnableConfig

from app.podcasts.schemas import PodcastSpec


@dataclass(kw_only=True)
class TranscriptConfig:
    """The approved spec and user focus that drive drafting."""

    workspace_id: int
    spec: PodcastSpec
    focus: str | None = None
    # A Studio audio overview runs on its Studio model; None is the chat model.
    llm: Any = None
    # Called with each segment's turns as soon as it is drafted, so audio can
    # be voiced while the next segment is written; None does nothing.
    on_segment: Any = None

    @classmethod
    def from_runnable_config(
        cls, config: RunnableConfig | None = None
    ) -> TranscriptConfig:
        configurable = (config.get("configurable") or {}) if config else {}
        names = {f.name for f in fields(cls) if f.init}
        return cls(**{k: v for k, v in configurable.items() if k in names})
