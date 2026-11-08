"""Trusted visual-style catalog for infographic generation."""

from __future__ import annotations

import re

from .schemas import ResolvedVisualStyle, VisualStylePreset

QUESTION_PRESET_ID = "infographic.visual-style"
QUESTION_PRESET_VERSION = 1
AUTO_STYLE_ID = "auto"
DEFAULT_STYLE_ID = "sketch-note"

KAWAII = VisualStylePreset(
    id="kawaii",
    version=1,
    label="Kawaii",
    preview_asset="infographic-style/kawaii",
    description=(
        "Use a playful kawaii illustration style with rounded shapes, pastel "
        "colors, thick clean outlines, friendly expressive icons, generous "
        "spacing, and highly legible infographic labels."
    ),
)

CLAY = VisualStylePreset(
    id="clay",
    version=1,
    label="Clay",
    preview_asset="infographic-style/clay",
    description=(
        "Use a tactile three-dimensional clay style with softly modeled forms, "
        "rounded edges, subtle handmade texture, gentle studio lighting, clear "
        "visual grouping, and highly legible infographic labels."
    ),
)

SKETCH_NOTE = VisualStylePreset(
    id="sketch-note",
    version=1,
    label="Sketch Note",
    preview_asset="infographic-style/sketch-note",
    description=(
        "Use a hand-drawn editorial sketchnote style with mostly black ink on "
        "a warm white background, one restrained accent color, simple icons, "
        "arrows, connectors, loose organic lines, generous whitespace, short "
        "hand-lettered headings, and highly legible labels. Avoid photorealism, "
        "dense paragraphs, decorative illegible handwriting, and watermarks."
    ),
)

ANIME = VisualStylePreset(
    id="anime",
    version=1,
    label="Anime",
    preview_asset="infographic-style/anime",
    description=(
        "Use a polished anime-inspired cel-shaded style with expressive "
        "characters or icons, clean linework, vivid controlled color, energetic "
        "composition, clear section hierarchy, and highly legible infographic "
        "labels."
    ),
)

EDITORIAL = VisualStylePreset(
    id="editorial",
    version=1,
    label="Editorial",
    preview_asset="infographic-style/editorial",
    description=(
        "Use a magazine editorial style: a large bold serif headline, clean "
        "sans-serif labels, a strong typographic hierarchy on an off-white "
        "page, black text with one bold accent colour, flat spot "
        "illustrations, thin rules, pull-quote numbers and a column grid with "
        "generous margins. Highly legible labels, no photographs."
    ),
)

INSTRUCTIONAL = VisualStylePreset(
    id="instructional",
    version=1,
    label="Instructional",
    preview_asset="infographic-style/instructional",
    description=(
        "Use a clear instructional-manual style: numbered steps or panels in "
        "an obvious reading order, simple flat line icons and diagrams, arrows "
        "that show sequence and cause, callout boxes for tips and warnings, a "
        "light neutral background with one blue accent and one orange "
        "highlight, and highly legible sans-serif labels."
    ),
)

BENTO_GRID = VisualStylePreset(
    id="bento-grid",
    version=1,
    label="Bento Grid",
    preview_asset="infographic-style/bento-grid",
    description=(
        "Use a modern bento-grid layout: the canvas divided into rounded "
        "rectangular tiles of different sizes with even gutters on a light "
        "background, one idea per tile, a large hero tile for the main point, "
        "big numbers in their own tiles, simple glyph icons, soft shadows, a "
        "cohesive muted palette with one vivid accent, and crisp sans-serif "
        "labels."
    ),
)

BRICKS = VisualStylePreset(
    id="bricks",
    version=1,
    label="Bricks",
    preview_asset="infographic-style/bricks",
    description=(
        "Use a toy building-brick style: every object, icon and chart built "
        "from glossy interlocking plastic bricks with round studs, in bright "
        "primary red, yellow, blue and green on a clean light background, "
        "soft studio lighting, labels printed on flat brick tiles or plates, "
        "playful but tidy composition, and highly legible text. No brand logos."
    ),
)

SCIENTIFIC = VisualStylePreset(
    id="scientific",
    version=1,
    label="Scientific",
    preview_asset="infographic-style/scientific",
    description=(
        "Use a scientific-figure style like a journal or conference poster: "
        "white background, precise vector schematics and labelled diagrams "
        "with leader lines, charts with axes and units, panel letters (A, B, "
        "C), a restrained colour-blind-safe palette of blues, teals and "
        "greys, thin strokes, and small but crisp sans-serif labels."
    ),
)

PROFESSIONAL = VisualStylePreset(
    id="professional",
    version=1,
    label="Professional",
    preview_asset="infographic-style/professional",
    description=(
        "Use a polished corporate style like a consulting report: clean flat "
        "design on white, navy and slate grey with one teal accent, a strict "
        "grid of aligned sections, simple line icons, clean bar or donut "
        "charts, bold KPI numbers with short captions, sans-serif typography, "
        "and highly legible labels. No clip art or decoration."
    ),
)

VISUAL_STYLE_PRESETS: tuple[VisualStylePreset, ...] = (
    KAWAII,
    CLAY,
    SKETCH_NOTE,
    ANIME,
    EDITORIAL,
    INSTRUCTIONAL,
    BENTO_GRID,
    BRICKS,
    SCIENTIFIC,
    PROFESSIONAL,
)
VISUAL_STYLE_BY_ID = {preset.id: preset for preset in VISUAL_STYLE_PRESETS}

_AUTO_RULES: tuple[tuple[frozenset[str], str], ...] = (
    (
        frozenset({"playful", "children", "child", "beginner", "friendly", "cute"}),
        KAWAII.id,
    ),
    (
        frozenset({"tactile", "product", "craft", "object", "physical", "handmade"}),
        CLAY.id,
    ),
    (
        frozenset(
            {"study", "notes", "brainstorming", "teaching", "concept", "explain"}
        ),
        SKETCH_NOTE.id,
    ),
    (
        frozenset({"entertainment", "gaming", "game", "manga", "dynamic"}),
        ANIME.id,
    ),
    (
        frozenset({"steps", "procedure", "instructions", "checklist", "workflow"}),
        INSTRUCTIONAL.id,
    ),
    (
        frozenset({"experiment", "research", "trial", "clinical", "scientific"}),
        SCIENTIFIC.id,
    ),
    (
        frozenset({"company", "business", "quarterly", "revenue", "market"}),
        PROFESSIONAL.id,
    ),
    (
        frozenset({"news", "story", "history", "magazine", "feature"}),
        EDITORIAL.id,
    ),
    (
        frozenset({"dashboard", "highlights", "summary", "metrics", "stats"}),
        BENTO_GRID.id,
    ),
    (
        frozenset({"toy", "toys", "building", "blocks", "bricks"}),
        BRICKS.id,
    ),
)
_WORD_PATTERN = re.compile(r"[a-z0-9]+")


def get_visual_style(style_id: str) -> VisualStylePreset:
    """Return a concrete style or reject unknown and selector-only IDs."""
    try:
        return VISUAL_STYLE_BY_ID[style_id]
    except KeyError:
        raise ValueError(f"Unknown infographic visual style: {style_id}") from None


def resolve_visual_style(requested_id: str, brief: str) -> ResolvedVisualStyle:
    """Resolve ``auto`` deterministically; explicit concrete IDs pass through."""
    if requested_id != AUTO_STYLE_ID:
        return ResolvedVisualStyle(
            requested_id=requested_id,
            preset=get_visual_style(requested_id),
        )

    words = frozenset(_WORD_PATTERN.findall(brief.casefold()))
    resolved_id = next(
        (style_id for signals, style_id in _AUTO_RULES if words & signals),
        DEFAULT_STYLE_ID,
    )
    return ResolvedVisualStyle(
        requested_id=AUTO_STYLE_ID,
        preset=VISUAL_STYLE_BY_ID[resolved_id],
    )


__all__ = [
    "ANIME",
    "AUTO_STYLE_ID",
    "BENTO_GRID",
    "BRICKS",
    "CLAY",
    "DEFAULT_STYLE_ID",
    "EDITORIAL",
    "INSTRUCTIONAL",
    "KAWAII",
    "PROFESSIONAL",
    "QUESTION_PRESET_ID",
    "QUESTION_PRESET_VERSION",
    "SCIENTIFIC",
    "SKETCH_NOTE",
    "VISUAL_STYLE_BY_ID",
    "VISUAL_STYLE_PRESETS",
    "get_visual_style",
    "resolve_visual_style",
]
