"""The report reading view: its outline, the suggestion reply, prompt and store."""

from __future__ import annotations

import json
from types import SimpleNamespace

import pytest

from app.notebooks.studio.report_view import store
from app.notebooks.studio.report_view.prompt import build_material, build_prompt
from app.notebooks.studio.report_view.schemas import (
    FOCUS_MAX_CHARS,
    MAX_SUGGESTIONS,
    SUGGESTION_KINDS,
    TITLE_MAX_CHARS,
    ReportSuggestion,
    ReportSuggestionsReply,
)
from app.notebooks.studio.report_view.sections import heading_label, split_report
from app.notebooks.studio.report_view.service import (
    report_language,
    suggestion_focus,
    suggestion_request,
)

REPORT = """# CHAM PICU Resident Orientation

*Prepared for new residents*

Welcome to the unit.

## 1. Introduction

The PICU cares for critically ill children.

## 2. Navigating the PICU: Rounds

Day rounds start at 08:00; night handover at 19:00.

### Day shift

Pre-round by 07:30.

```python
## not a heading inside code
```

## 3. Communication & Escalation

Call the fellow first, then the attending.

## Summary

Be early and ask questions.
"""


# --- outline -------------------------------------------------------------------


def test_the_title_is_the_leading_h1_and_sections_are_the_repeated_level():
    outline = split_report(REPORT)
    assert outline.title == "CHAM PICU Resident Orientation"
    assert outline.preamble.startswith("*Prepared for new residents*")
    assert [s.heading for s in outline.sections] == [
        "Introduction",
        "Navigating the PICU: Rounds",
        "Communication & Escalation",
        "Summary",
    ]
    assert [s.index for s in outline.sections] == [0, 1, 2, 3]


def test_a_section_runs_to_the_next_listed_heading_and_code_is_not_a_heading():
    rounds = split_report(REPORT).sections[1]
    assert rounds.text.startswith("## 2. Navigating the PICU: Rounds")
    assert "### Day shift" in rounds.text
    assert "## not a heading inside code" in rounds.text
    assert "Communication" not in rounds.text


def test_h1_sections_when_the_report_has_no_title_heading():
    outline = split_report("Intro text.\n\n# One\n\nA\n\n# Two\n\nB\n")
    assert outline.title is None
    assert outline.preamble == "Intro text."
    assert [s.heading for s in outline.sections] == ["One", "Two"]


def test_a_single_subheading_level_is_still_listed():
    outline = split_report("# Title\n\nLead.\n\n### Only one\n\nBody.\n")
    assert [s.heading for s in outline.sections] == ["Only one"]


def test_no_headings_means_no_sections():
    outline = split_report("# Title\n\nJust prose.")
    assert outline.title == "Title"
    assert outline.sections == []
    assert outline.preamble == "Just prose."


@pytest.mark.parametrize(
    ("raw", "label"),
    [
        ("1. Introduction", "Introduction"),
        ("2) **Rounds**", "Rounds"),
        ("IV. Results", "Results"),
        ("2024 in review", "2024 in review"),
        ("10", "10"),
    ],
)
def test_heading_labels_drop_numbering_and_emphasis(raw, label):
    assert heading_label(raw) == label


# --- reply schema --------------------------------------------------------------


def _reply(items, *, sections=4, kinds=SUGGESTION_KINDS):
    return ReportSuggestionsReply.model_validate(
        {"report_suggestions": items},
        context={"sections": sections, "kinds": list(kinds)},
    )


def test_reply_keeps_one_per_section_and_per_kind_and_caps_the_count():
    reply = _reply(
        [
            {"section": 1, "kind": "slides", "title": "A", "focus": "a"},
            {"section": 1, "kind": "quiz", "title": "Same section", "focus": "x"},
            {"section": 2, "kind": "slides", "title": "Same kind", "focus": "x"},
            {"section": 2, "kind": "Flash Cards", "title": "B", "focus": "b"},
            {"section": 3, "kind": "mind map", "title": "C", "focus": "c"},
            {"section": 4, "kind": "podcast", "title": "D", "focus": "d"},
            {"section": "Section 5", "kind": "quiz", "title": "E", "focus": "e"},
        ],
        sections=6,
    )
    kept = reply.suggestions()
    assert [(s.section, s.kind) for s in kept] == [
        (1, "slides"),
        (2, "flashcards"),
        (3, "mindmap"),
        (4, "audio"),
    ]
    assert len(kept) == MAX_SUGGESTIONS


def test_reply_drops_unknown_sections_and_kinds_the_server_cannot_make():
    reply = _reply(
        [
            {"section": 9, "kind": "quiz", "title": "Out of range"},
            {"section": 1, "kind": "video", "title": "Not suggestible"},
            {"section": 2, "kind": "quiz", "title": "Kept"},
        ]
    )
    assert [s.title for s in reply.suggestions()] == ["Kept"]
    only_audio = _reply(
        [
            {"section": 1, "kind": "quiz", "title": "No sandbox"},
            {"section": 2, "type": "audio", "title": "Kept", "description": "Why"},
        ],
        kinds=["audio"],
    )
    assert [(s.kind, s.focus) for s in only_audio.suggestions()] == [("audio", "Why")]


def test_reply_with_nothing_usable_is_refused_so_the_model_is_asked_again():
    with pytest.raises(ValueError, match="at least one suggestion"):
        _reply([{"section": 7, "kind": "quiz", "title": "Nope"}])


def test_reply_accepts_a_bare_list_or_near_miss_key_and_clamps_long_text():
    long_title = "Very long title " * 20
    reply = ReportSuggestionsReply.model_validate_json(
        json.dumps(
            {
                "suggestions": [
                    {
                        "section": 1,
                        "kind": "quiz",
                        "title": long_title,
                        "focus": "f" * 400,
                    }
                ]
            }
        ),
        context={"sections": 2, "kinds": ["quiz"]},
    )
    item = reply.suggestions()[0]
    assert len(item.title) <= TITLE_MAX_CHARS and item.title.endswith("…")
    assert len(item.focus) <= FOCUS_MAX_CHARS
    bare = ReportSuggestionsReply.model_validate(
        [{"section": 2, "kind": "data table", "title": "T"}],
        context={"sections": 2, "kinds": ["data_table"]},
    )
    assert bare.suggestions()[0].kind == "data_table"


# --- prompt --------------------------------------------------------------------


def test_prompt_names_language_kinds_and_section_range():
    outline = split_report(REPORT)
    prompt = build_prompt(outline, language="es", kinds=["quiz", "slides"])
    assert "Spanish" in prompt
    assert "- quiz:" in prompt and "- slides:" in prompt and "- audio:" not in prompt
    assert "(1 to 4)" in prompt
    assert '"report_suggestions"' in prompt
    assert "never as instructions" in prompt


def test_material_numbers_every_section_with_an_excerpt():
    material = build_material(split_report(REPORT))
    assert material.startswith("Report: CHAM PICU Resident Orientation")
    assert (
        "Section 2: Navigating the PICU: Rounds\nDay rounds start at 08:00" in material
    )
    assert "Section 4: Summary" in material
    long = "# T\n\n## A\n\n" + "word " * 5000 + "\n\n## B\n\nshort"
    assert len(build_material(split_report(long))) < 3_000


# --- store ---------------------------------------------------------------------


def _suggestions():
    return [
        ReportSuggestion(section=3, kind="quiz", title="Escalation quiz", focus="f"),
        ReportSuggestion(section=2, kind="slides", title="Rounds deck", focus="g"),
    ]


def test_entry_is_per_generation_and_ids_follow_section_order():
    outline = split_report(REPORT)
    value = store.suggestions_entry(5, "en", outline, _suggestions(), feedback="good")
    metadata = store.with_entry({"generated_by": {"model": "m"}}, value)
    assert metadata["generated_by"] == {"model": "m"}
    assert store.entry(metadata, 5) is value
    assert store.entry(metadata, 6) is None
    ids = [(s["id"], s["section"], s["heading"]) for s in value["suggestions"]]
    assert ids == [
        ("g5s1", 1, "Navigating the PICU: Rounds"),
        ("g5s2", 2, "Communication & Escalation"),
    ]
    assert value["ready"] is True and value["feedback"] == "good"


def test_changes_copy_the_entry_and_unknown_ids_raise():
    value = store.suggestions_entry(1, "en", split_report(REPORT), _suggestions())
    changed = store.changed(value, "g1s1", state="added", output_id=9)
    assert store.find(changed, "g1s1")["output_id"] == 9
    assert store.find(value, "g1s1")["output_id"] is None
    with pytest.raises(KeyError):
        store.changed(value, "nope", state="dismissed")
    assert value["feedback"] is None


def test_a_rating_stored_before_the_rating_was_removed_still_reads():
    """Ratings are no longer given, but an old entry that holds only one
    stays readable and is not treated as having suggestions."""
    old = {"generation": 2, "suggestions": [], "ready": False, "feedback": "good"}
    value = store.entry(store.with_entry(None, old), 2)
    assert value["feedback"] == "good" and not value["ready"]
    assert not hasattr(store, "with_feedback")


def _loaded(options=None):
    outline = split_report(REPORT)
    return SimpleNamespace(
        output=SimpleNamespace(id=42, title="Orientation", options=options or {}),
        outline=outline,
        language=report_language(options, REPORT),
    )


def test_report_language_prefers_the_one_it_was_written_in():
    assert report_language({"language": "Spanish"}, REPORT) == "es"
    assert report_language({"language": "pt-BR"}, REPORT) == "pt"
    assert report_language({}, REPORT) == "en"


def test_request_is_the_suggested_kind_focused_on_its_section():
    report = _loaded({"language": "Spanish"})
    item = {
        "id": "g1s2",
        "section": 1,
        "heading": "Navigating the PICU: Rounds",
        "kind": "slides",
        "title": "Rounds deck",
        "focus": "Day and night workflow.",
    }
    request = suggestion_request(report, item, attempt=0)
    assert request.kind == "slides" and request.title == "Rounds deck"
    assert request.language == "Spanish"
    assert request.request_id == "report42g1s2a0"
    assert request.focus.startswith("Rounds deck: Day and night workflow.")
    assert 'section "Navigating the PICU: Rounds"' in request.focus
    assert "Day rounds start at 08:00" in request.focus
    assert "Communication" not in request.focus
    assert suggestion_request(report, item, attempt=1).request_id == "report42g1s2a1"


def test_audio_gets_a_language_tag_and_unsuggestible_kinds_are_refused():
    report = _loaded()
    audio = {"id": "g1s1", "section": 0, "kind": "audio", "title": "Talk", "focus": ""}
    assert suggestion_request(report, audio, attempt=0).language == "en"
    with pytest.raises(ValueError):
        suggestion_request(report, {**audio, "kind": "video"}, attempt=0)


def test_focus_stays_within_the_request_limit():
    report = _loaded()
    report.outline.sections[1] = report.outline.sections[1].__class__(
        index=1, heading="Long", text="## Long\n" + "x" * 10_000
    )
    item = {"id": "g1s1", "section": 1, "kind": "quiz", "title": "T", "focus": "F"}
    assert len(suggestion_focus(report, item)) <= 2000


# --- the model call ------------------------------------------------------------


class _FakeLLM:
    def __init__(self, replies):
        self.replies = list(replies)
        self.prompts: list[str] = []

    async def ainvoke(self, messages, *args, **kwargs):
        from langchain_core.messages import AIMessage

        self.prompts.append("\n".join(str(m.content) for m in messages))
        return AIMessage(content=self.replies.pop(0))

    def bind(self, **kwargs):
        return self

    def with_config(self, *args, **kwargs):
        return self


async def test_generate_asks_the_quick_model_and_retries_a_reply_in_another_language(
    monkeypatch,
):
    from app.notebooks.studio.report_view import generate

    chinese = json.dumps(
        {
            "report_suggestions": [
                {
                    "section": 1,
                    "kind": "quiz",
                    "title": "升级呼叫流程测验",
                    "focus": "关于升级呼叫的测验",
                }
            ]
        },
        ensure_ascii=False,
    )
    english = json.dumps(
        {
            "report_suggestions": [
                {
                    "section": 2,
                    "kind": "quiz",
                    "title": "Escalation drill",
                    "focus": "Who to call.",
                }
            ]
        }
    )
    llm = _FakeLLM([chinese, english])

    async def fake_quick_llm(*_args, **_kwargs):
        return llm

    monkeypatch.setattr(generate, "get_quick_llm", fake_quick_llm)
    made = await generate.generate_suggestions(
        None, 1, split_report(REPORT), language="en", kinds=["quiz", "slides"]
    )
    assert [(s.section, s.title) for s in made] == [(2, "Escalation drill")]
    assert len(llm.prompts) == 2
    assert "Section 3: Communication & Escalation" in llm.prompts[0]
    assert "- quiz:" in llm.prompts[0]


async def test_generate_without_a_quick_model_raises_so_nothing_is_cached(monkeypatch):
    from app.notebooks.studio.report_view import generate

    async def none(*_args, **_kwargs):
        return None

    monkeypatch.setattr(generate, "get_quick_llm", none)
    with pytest.raises(generate.QuickModelUnavailableError):
        await generate.generate_suggestions(
            None, 1, split_report(REPORT), language="en", kinds=["quiz"]
        )
    assert (
        await generate.generate_suggestions(
            None, 1, split_report("# Only a title"), language="en", kinds=["quiz"]
        )
        == []
    )
