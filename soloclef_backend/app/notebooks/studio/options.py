"""What each studio tile's dialog can ask for.

One request model per output, discriminated by ``kind``. The option values are
presets, not free numbers, so the dialog and the generators agree on a small,
tested vocabulary.
"""

from __future__ import annotations

from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

_MAX_FOCUS_CHARS = 2000
_MAX_TEMPLATE_PROMPT_CHARS = 4000


class _StudioRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_ids: list[int] | None = Field(
        default=None,
        max_length=500,
        description="Notebook source ids to generate from; omitted means the enabled ones",
    )
    focus: str = Field(default="", max_length=_MAX_FOCUS_CHARS)
    title: str | None = Field(default=None, max_length=200)
    language: str | None = Field(default=None, max_length=40)
    # A client-generated id so a double click cannot start two generations.
    request_id: str | None = Field(
        default=None, max_length=64, pattern=r"^[A-Za-z0-9_-]+$"
    )

    @field_validator("focus")
    @classmethod
    def _strip_focus(cls, value: str) -> str:
        return value.strip()


class AudioRequest(_StudioRequest):
    kind: Literal["audio"]
    format: Literal["deep_dive", "brief", "critique", "debate"] = "deep_dive"
    length: Literal["short", "default", "long"] = "default"


VideoStyle = Literal[
    "auto", "custom", "classic", "whiteboard", "playful", "dark", "minimal"
]
_MAX_STYLE_PROMPT_CHARS = 300


class VideoRequest(_StudioRequest):
    kind: Literal["video"]
    format: Literal["short", "explainer"] = "explainer"
    visual_style: VideoStyle = "auto"
    # Only with ``visual_style="custom"``: the user's own short description.
    style_prompt: str | None = Field(default=None, max_length=_MAX_STYLE_PROMPT_CHARS)

    @model_validator(mode="after")
    def _custom_style_needs_words(self) -> VideoRequest:
        text = " ".join((self.style_prompt or "").split())
        if self.visual_style == "custom" and not text:
            raise ValueError("a custom visual style needs a description")
        self.style_prompt = text if self.visual_style == "custom" else None
        return self


class StudyRequest(_StudioRequest):
    kind: Literal["quiz", "flashcards"]
    count: Literal["fewer", "standard", "more"] = "standard"
    difficulty: Literal["easy", "medium", "hard"] = "medium"


class MindmapRequest(_StudioRequest):
    kind: Literal["mindmap"]
    detail: Literal["overview", "standard", "detailed"] = "standard"


class SlidesRequest(_StudioRequest):
    kind: Literal["slides"]
    format: Literal["detailed", "presenter"] = "detailed"
    length: Literal["short", "default", "long"] = "default"


class DataTableRequest(_StudioRequest):
    kind: Literal["data_table"]


InfographicStyle = Literal[
    "auto",
    "kawaii",
    "clay",
    "sketch-note",
    "anime",
    "editorial",
    "instructional",
    "bento-grid",
    "bricks",
    "scientific",
    "professional",
]


class InfographicRequest(_StudioRequest):
    kind: Literal["infographic"]
    # The chat asks this inside the tool; a studio run is headless, so the
    # dialog asks it instead. The ids are the preset catalogue's.
    style: InfographicStyle = "auto"
    # The image size and layout (app/artifacts/infographic/layout.py).
    orientation: Literal["landscape", "portrait", "square"] = "landscape"
    # How much the plan holds and how many planned items the check reads.
    detail: Literal["concise", "standard", "detailed"] = "standard"


class ReportRequest(_StudioRequest):
    kind: Literal["report"]
    format: Literal["interactive", "document"] = "document"
    file_type: Literal["pdf", "docx"] = "pdf"
    template_key: str | None = Field(default=None, max_length=64)
    template_prompt: str | None = Field(
        default=None, max_length=_MAX_TEMPLATE_PROMPT_CHARS
    )


StudioRequest = Annotated[
    AudioRequest
    | VideoRequest
    | StudyRequest
    | MindmapRequest
    | SlidesRequest
    | DataTableRequest
    | InfographicRequest
    | ReportRequest,
    Field(discriminator="kind"),
]

StudioKind = Literal[
    "audio",
    "video",
    "report",
    "slides",
    "mindmap",
    "flashcards",
    "quiz",
    "data_table",
    "infographic",
]

STUDIO_KINDS: tuple[str, ...] = (
    "audio",
    "video",
    "report",
    "slides",
    "mindmap",
    "flashcards",
    "quiz",
    "data_table",
    "infographic",
)
