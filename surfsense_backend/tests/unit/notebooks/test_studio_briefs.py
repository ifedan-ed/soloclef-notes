"""The studio dialog's presets map onto generator inputs as documented."""

from __future__ import annotations

import pytest
from pydantic import TypeAdapter, ValidationError

from app.notebooks.chat.followups import parse_questions
from app.notebooks.studio import briefs
from app.notebooks.studio.options import StudioRequest
from app.notebooks.studio.source_material import SourceDocument, compile_material
from app.notebooks.studio.suggestions import suggestions_key
from app.podcasts.schemas import PodcastStyle

pytestmark = pytest.mark.unit

_parse = TypeAdapter(StudioRequest).validate_python


@pytest.mark.parametrize(
    ("fmt", "style", "speakers"),
    [
        ("deep_dive", PodcastStyle.CONVERSATIONAL, 2),
        ("brief", PodcastStyle.MONOLOGUE, 1),
        ("critique", PodcastStyle.INTERVIEW, 2),
        ("debate", PodcastStyle.DEBATE, 2),
    ],
)
def test_audio_formats(fmt, style, speakers):
    plan = briefs.audio_plan(_parse({"kind": "audio", "format": fmt}))
    assert (plan.style, plan.speaker_count) == (style, speakers)


def test_audio_length_and_focus():
    plan = briefs.audio_plan(
        _parse({"kind": "audio", "length": "long", "focus": "  the methods  "})
    )
    # Long runs as far as the material supports, up to 30 minutes, never padded.
    assert (plan.min_seconds, plan.max_seconds) == (600, 1800)
    assert plan.focus == "the methods"


def test_unknown_kind_or_preset_is_rejected():
    with pytest.raises(ValidationError):
        _parse({"kind": "poster"})
    with pytest.raises(ValidationError):
        _parse({"kind": "infographic", "style": "photoreal"})
    with pytest.raises(ValidationError):
        _parse({"kind": "quiz", "count": "lots"})


def test_quiz_prompt_carries_count_difficulty_and_material():
    request = _parse({"kind": "quiz", "count": "fewer", "difficulty": "easy"})
    prompt = briefs.artifact_prompt(request, title="T", material="MATERIAL")
    assert "exactly 5 multiple-choice questions" in prompt and "easy" in prompt
    assert "<source_material>\nMATERIAL\n</source_material>" in prompt
    assert briefs.artifact_output(request) == "quiz"


def test_report_formats():
    interactive = _parse(
        {"kind": "report", "format": "interactive", "template_prompt": "Do X."}
    )
    document = _parse({"kind": "report", "format": "document", "file_type": "docx"})
    assert "HTML" in briefs.artifact_prompt(interactive, title="T", material="m")
    assert "Do X." in briefs.artifact_prompt(interactive, title="T", material="m")
    assert "Word (.docx)" in briefs.artifact_prompt(document, title="T", material="m")


def test_video_brief_format_and_language():
    request = _parse({"kind": "video", "format": "short", "language": "German"})
    brief = briefs.video_brief(request)
    assert "60 to 90 seconds" in brief and "German" in brief
    assert "source material supplied with this brief" in brief


def test_material_shares_the_budget_fairly():
    documents = [
        SourceDocument(id=1, title="Short", text="tiny"),
        SourceDocument(id=2, title="Long A", text="a" * 1000),
        SourceDocument(id=3, title="Long B", text="b" * 1000),
    ]
    material = compile_material(documents, 600)
    assert "tiny" in material
    # Each long source keeps a fair share, condensed rather than head-cut.
    assert material.count("a") >= 150 and material.count("b") >= 150
    assert len(material) <= 700


def test_suggestions_are_parsed_defensively():
    assert parse_questions('Sure: ["One", "Two", " ", "Three", "Four"]') == [
        "One",
        "Two",
        "Three",
    ]
    assert parse_questions("no json here") == []
    assert suggestions_key([2, 1]) == suggestions_key([1, 2])


def test_infographic_defaults_to_auto_and_keeps_the_chosen_style():
    assert briefs.infographic_style(_parse({"kind": "infographic"})) == "auto"
    request = _parse({"kind": "infographic", "style": "sketch-note"})
    assert briefs.artifact_output(request) == "infographic"
    assert briefs.infographic_style(request) == "sketch-note"
    prompt = briefs.artifact_prompt(request, title="T", material="MATERIAL")
    assert "infographic" in prompt and "do not ask" in prompt
    assert briefs.infographic_style(_parse({"kind": "quiz"})) is None


@pytest.mark.parametrize(
    ("style", "expected"),
    [
        ("classic", "Visual style: Classic"),
        ("whiteboard", "Visual style: Whiteboard"),
        ("playful", "Visual style: Playful"),
        ("dark", "Visual style: Dark"),
        ("minimal", "Visual style: Minimal"),
    ],
)
def test_each_named_video_style_reaches_the_brief(style, expected):
    brief = briefs.video_brief(_parse({"kind": "video", "visual_style": style}))
    assert expected in brief
    # Nothing a style asks for is outside the renderer's three fonts.
    line = briefs.video_style_line(_parse({"kind": "video", "visual_style": style}))
    for font in ("Comic", "Roboto", "Helvetica", "handwriting font"):
        assert font not in line


def test_auto_video_style_leaves_the_look_to_the_author():
    request = _parse({"kind": "video"})
    assert request.visual_style == "auto"
    assert briefs.video_style_line(request) is None
    assert "Visual style" not in briefs.video_brief(request)


def test_custom_video_style_carries_the_users_words_and_needs_some():
    request = _parse(
        {
            "kind": "video",
            "visual_style": "custom",
            "style_prompt": "  retro  80s neon\n grid ",
        }
    )
    assert request.style_prompt == "retro 80s neon grid"
    assert "retro 80s neon" in briefs.video_brief(request)
    with pytest.raises(ValidationError):
        _parse({"kind": "video", "visual_style": "custom", "style_prompt": "   "})
    with pytest.raises(ValidationError):
        _parse({"kind": "video", "visual_style": "sepia"})
    # A description sent with a named style is ignored, not smuggled in.
    named = _parse({"kind": "video", "visual_style": "dark", "style_prompt": "x"})
    assert named.style_prompt is None


@pytest.mark.parametrize(
    ("kind", "size", "target", "minimum"),
    [
        ("flashcards", "fewer", 10, 9),
        ("flashcards", "standard", 20, 18),
        ("flashcards", "more", 35, 32),
        ("quiz", "fewer", 5, 5),
        ("quiz", "standard", 10, 9),
        ("quiz", "more", 20, 18),
    ],
)
def test_study_sizes_are_exact_numbers_with_a_narrow_floor(kind, size, target, minimum):
    request = _parse({"kind": kind, "count": size})

    count = briefs.item_count(request)
    prompt = briefs.artifact_prompt(request, title="T", material="M")

    assert (count.target, count.minimum) == (target, minimum)
    assert f"exactly {target} " in prompt
    assert f"more than {target} are cut at save" in prompt


def test_only_study_outputs_carry_an_item_count():
    assert briefs.item_count(_parse({"kind": "mindmap"})) is None


def test_infographic_defaults_to_a_landscape_standard_canvas():
    request = _parse({"kind": "infographic"})
    assert (request.orientation, request.detail) == ("landscape", "standard")
    assert briefs.infographic_job_fields(request) == {
        "infographic_orientation": "landscape",
        "infographic_detail": "standard",
        "infographic_description": None,
    }
    assert briefs.infographic_job_fields(_parse({"kind": "quiz"})) == {}


@pytest.mark.parametrize("style", ["editorial", "bento-grid", "bricks", "professional"])
def test_infographic_accepts_the_new_styles(style):
    assert (
        briefs.infographic_style(_parse({"kind": "infographic", "style": style}))
        == style
    )


@pytest.mark.parametrize(
    ("field", "value"), [("orientation", "panorama"), ("detail", "exhaustive")]
)
def test_infographic_layout_values_outside_the_presets_are_refused(field, value):
    with pytest.raises(ValidationError):
        _parse({"kind": "infographic", field: value})


def test_infographic_orientation_and_detail_shape_the_plan_in_the_prompt():
    portrait = briefs.artifact_prompt(
        _parse({"kind": "infographic", "orientation": "portrait", "detail": "concise"}),
        title="T",
        material="m",
    )
    assert "tall portrait page (2:3)" in portrait
    assert "Plan 3 H2 sections" in portrait

    detailed = briefs.artifact_prompt(
        _parse({"kind": "infographic", "orientation": "square", "detail": "detailed"}),
        title="T",
        material="m",
    )
    assert "square page (1:1)" in detailed
    assert "Plan 6 or 7 H2 sections" in detailed


def test_infographic_description_reaches_the_image_model_verbatim():
    request = _parse(
        {
            "kind": "infographic",
            "focus": "  Use a blue color theme and highlight the 3 key stats ",
        }
    )
    fields = briefs.infographic_job_fields(request)
    assert (
        fields["infographic_description"]
        == "Use a blue color theme and highlight the 3 key stats"
    )
