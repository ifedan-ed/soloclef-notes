"""Canvas shape and level of detail an infographic is generated with.

The Studio dialog asks for both (NotebookLM's "Choose orientation" and
"Level of detail"). Orientation becomes a real image size the image model
accepts (gpt-image-2 and its fallback take 1536x1024, 1024x1536 and
1024x1024) plus a layout the Markdown plan and the image prompt both follow.
Detail sets how much the plan holds, how dense the image is, and how many of
the planned items the text check reads back. A chat run sets neither and
keeps the model's own canvas and the full check.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

Orientation = Literal["landscape", "portrait", "square"]
Detail = Literal["concise", "standard", "detailed"]

ORIENTATIONS: tuple[str, ...] = ("landscape", "portrait", "square")
DETAILS: tuple[str, ...] = ("concise", "standard", "detailed")
DEFAULT_ORIENTATION: Orientation = "landscape"
DEFAULT_DETAIL: Detail = "standard"


@dataclass(frozen=True, slots=True)
class Canvas:
    orientation: str
    width: int
    height: int
    plan: str
    """How the Markdown plan should be arranged for this shape."""
    composition: str
    """How the image model should compose the page."""

    @property
    def size(self) -> str:
        return f"{self.width}x{self.height}"


_CANVASES: dict[str, Canvas] = {
    "landscape": Canvas(
        orientation="landscape",
        width=1536,
        height=1024,
        plan=(
            "Plan it for a wide landscape page (3:2): a title band across the "
            "top, then the sections side by side in three or four columns or "
            "as a left-to-right flow."
        ),
        composition=(
            "Landscape canvas, 1536x1024 pixels (3:2, wider than tall). Put the "
            "title in a band across the top and arrange the sections side by "
            "side in columns or a left-to-right flow; use the full width."
        ),
    ),
    "portrait": Canvas(
        orientation="portrait",
        width=1024,
        height=1536,
        plan=(
            "Plan it for a tall portrait page (2:3): the title at the top, then "
            "the sections stacked one below another in reading order."
        ),
        composition=(
            "Portrait canvas, 1024x1536 pixels (2:3, taller than wide). Put the "
            "title at the top and stack the sections vertically in reading "
            "order; use the full height."
        ),
    ),
    "square": Canvas(
        orientation="square",
        width=1024,
        height=1024,
        plan=(
            "Plan it for a square page (1:1): the title at the top and the "
            "sections in a balanced two-by-two grid or around a central idea."
        ),
        composition=(
            "Square canvas, 1024x1024 pixels (1:1). Put the title at the top "
            "and balance the sections in a two-by-two grid or around a "
            "central focal point."
        ),
    ),
}


@dataclass(frozen=True, slots=True)
class DetailLevel:
    detail: str
    sections: str
    points: str
    max_checked_items: int
    """How many planned items the text check reads back from the image."""
    density: str
    """How dense the image model should make the page."""

    def plan(self) -> str:
        return (
            f"Level of detail: {self.detail}. Plan {self.sections} H2 sections "
            f"under the H1 title, each with {self.points}. Keep every label "
            "short enough to print legibly."
        )


_DETAILS: dict[str, DetailLevel] = {
    "concise": DetailLevel(
        detail="concise",
        sections="3",
        points="at most 2 short bullet labels and at most one key number",
        max_checked_items=12,
        density=(
            "Concise: only the title, the section headings, their few short "
            "labels and key numbers; large type and generous white space."
        ),
    ),
    "standard": DetailLevel(
        detail="standard",
        sections="4 or 5",
        points="2 or 3 short bullet labels and the key numbers that support them",
        max_checked_items=20,
        density=(
            "Standard: every section heading with its short labels and key "
            "numbers, balanced with illustration and white space."
        ),
    ),
    "detailed": DetailLevel(
        detail="detailed",
        sections="6 or 7",
        points=(
            "3 or 4 bullet labels, the supporting numbers, and one short "
            "explanatory phrase where it helps"
        ),
        max_checked_items=30,
        density=(
            "Detailed: every section, label, number and short phrase in the "
            "content, organised in a clear grid with smaller but still legible "
            "type; nothing may be dropped to save space."
        ),
    ),
}


def canvas_for(orientation: str | None) -> Canvas | None:
    """The canvas for an orientation; ``None`` keeps the model's default."""
    if orientation is None:
        return None
    try:
        return _CANVASES[orientation]
    except KeyError:
        raise ValueError(f"Unknown infographic orientation: {orientation}") from None


def detail_for(detail: str | None) -> DetailLevel | None:
    """The detail level; ``None`` leaves the plan's size to the author."""
    if detail is None:
        return None
    try:
        return _DETAILS[detail]
    except KeyError:
        raise ValueError(f"Unknown infographic level of detail: {detail}") from None


@dataclass(frozen=True, slots=True)
class InfographicLayout:
    """What a headless (Studio) run already chose besides the style."""

    orientation: str | None = None
    detail: str | None = None
    description: str | None = None
    """The user's own words about the image ("use a blue theme")."""

    def __post_init__(self) -> None:
        canvas_for(self.orientation)
        detail_for(self.detail)

    def plan_instructions(self) -> str | None:
        """What the Markdown plan must look like, for the authoring model."""
        lines = []
        canvas = canvas_for(self.orientation)
        level = detail_for(self.detail)
        if canvas is not None:
            lines.append(canvas.plan)
        if level is not None:
            lines.append(level.plan())
        return "\n".join(lines) or None


def image_directions(
    *, orientation: str | None, detail: str | None, description: str | None
) -> str | None:
    """The canvas, density and user request sections of the image prompt."""
    sections = []
    canvas = canvas_for(orientation)
    level = detail_for(detail)
    if canvas is not None:
        sections.append(f"CANVAS\n{canvas.composition}")
    if level is not None:
        sections.append(f"LEVEL OF DETAIL\n{level.density}")
    text = " ".join((description or "").split())
    if text:
        sections.append(
            "THE USER ASKED FOR\n"
            f"{text}\n"
            "Follow these wishes for colour, emphasis and composition unless they "
            "contradict the content or the style."
        )
    return "\n\n".join(sections) or None


__all__ = [
    "DEFAULT_DETAIL",
    "DEFAULT_ORIENTATION",
    "DETAILS",
    "ORIENTATIONS",
    "Canvas",
    "Detail",
    "DetailLevel",
    "InfographicLayout",
    "Orientation",
    "canvas_for",
    "detail_for",
    "image_directions",
]
