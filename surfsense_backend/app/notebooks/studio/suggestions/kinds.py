"""What each Studio dialog's suggestions are, per output type and format.

A kind names what is being made, the schema its ideas come back in, and a
sample reply. The sample is validated against the schema at import, and the
prompt shows the validated form, so the two cannot drift: a schema change
that breaks a sample fails on import, in every test run.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .schemas import (
    Angles,
    IdeaPool,
    KeyMessages,
    MapOutlines,
    Questions,
    ShowFocuses,
    StudyTargets,
    TableDesigns,
    Topics,
)

GENERAL = "general"


@dataclass(frozen=True)
class SuggestionKind:
    purpose: str
    schema: type[IdeaPool]
    sample: IdeaPool
    # Extra guidance on top of the schema's, per format the dialog offers.
    variants: dict[str, str] = field(default_factory=dict)


def _kind(
    purpose: str,
    schema: type[IdeaPool],
    ideas: list[str],
    example: str,
    variants: dict[str, str] | None = None,
) -> SuggestionKind:
    sample = schema.model_validate({schema.ideas_field: ideas, "examples": [example]})
    return SuggestionKind(purpose, schema, sample, variants or {})


# Samples are about a subject no notebook is likely to hold (a school sleep
# trial), so the model borrows their shape and not their content.
_STUDY_SAMPLE = [
    "Definitions of sleep latency, efficiency and debt, as one-line recall",
    "Applying the dose-response result to a new school's timetable, advanced",
    "Common misreadings of the effect size, as true-or-false statements",
]
_SHOW_SAMPLE = [
    "Why 34 extra minutes of sleep moved grades, and how sure the trial can be",
    "What the dropout pattern says about who the program actually reached",
    "The cost per student against the attendance gained",
]
_BRIEF_SAMPLE = [
    "For school boards: the case for a later start, limited to the trial's outcomes",
    "For parents: what changes at home, from bedtimes to screens",
    "For researchers: the design's weak points and what a replication should fix",
]

# A notebook's new chat: questions to start the conversation with.
CHAT = "chat"

KINDS: dict[str, SuggestionKind] = {
    CHAT: _kind(
        "questions to start a chat about the sources",
        Questions,
        [
            "How many extra minutes of sleep did the later start actually give?",
            "Why did grades improve more than attendance?",
            "What would a replication of this trial need to fix?",
        ],
        "Which students gained least from the program, and why?",
    ),
    GENERAL: _kind(
        "an overview of the sources",
        Topics,
        [
            "How the later start changed sleep, grades and attendance",
            "Who gained least from the program, and why",
            "What the trial could not rule out",
        ],
        "The main findings and the limits the authors admit",
    ),
    "data_table": _kind(
        "a data table extracting structured facts from the sources",
        TableDesigns,
        [
            "One row per outcome: measure, effect size, 95% CI, follow-up",
            "One row per school: region, start time, minutes of sleep gained",
            "One row per limitation: what it is, how it biases results, a fix",
        ],
        "One row per outcome with the measure, the effect and its confidence interval",
    ),
    "flashcards": _kind(
        "a set of study flashcards drawn from the sources",
        StudyTargets,
        _STUDY_SAMPLE,
        "The key definitions and the main effect sizes, at an introductory level",
        {
            "easy": "Pitch them at a newcomer: recall of terms and headline numbers.",
            "medium": "Pitch them at a student who has read the sources once.",
            "hard": "Pitch them at an expert: application, edge cases, nuance.",
        },
    ),
    "quiz": _kind(
        "a multiple-choice quiz on the sources",
        StudyTargets,
        _STUDY_SAMPLE,
        "Scenario questions on applying the main result, not recall of numbers",
        {
            "easy": "Pitch it at a newcomer: recall of terms and headline numbers.",
            "medium": "Pitch it at a student who has read the sources once.",
            "hard": "Pitch it at an expert: application, edge cases, nuance.",
        },
    ),
    "mindmap": _kind(
        "a mind map of the sources",
        MapOutlines,
        [
            "Later school start: sleep gained, grades, attendance, costs",
            "Why teens sleep late: biology, screens, schedules, homework",
            "Trial design: sampling, measures, dropout, confounders",
        ],
        "Center it on the sleep program, with branches for outcomes and limits",
    ),
    "report": _kind(
        "a report based on the sources",
        Angles,
        _BRIEF_SAMPLE,
        "A briefing for school principals on what the trial found and what it costs",
        {
            "document": "It is a written document someone reads start to finish.",
            "interactive": "It is an interactive web report people explore.",
        },
    ),
    "slides": _kind(
        "a slide deck presenting the sources",
        Angles,
        _BRIEF_SAMPLE,
        "A 10-minute talk for teachers on the trial's main result and its limits",
        {
            "detailed": "The deck is read on its own, so it carries the detail.",
            "presenter": "Someone presents the deck, so slides stay sparse.",
        },
    ),
    "audio": _kind(
        "a two-host audio discussion of the sources",
        ShowFocuses,
        _SHOW_SAMPLE,
        "Walk through the trial's main result and what it means for parents",
        {
            "deep_dive": (
                "It is a deep dive: each idea is a thread the hosts can dig "
                "into for minutes, a finding with its evidence and meaning."
            ),
            "brief": (
                "It is a short brief: each idea is the single point a "
                "two-minute summary should get across."
            ),
            "critique": (
                "It is a critique: each idea is something the hosts should "
                "scrutinise, such as a method, an assumption or a gap."
            ),
            "debate": (
                "It is a debate: each idea is a contested question the two "
                "hosts can argue from opposite sides, written as a question "
                "ending in '?'."
            ),
        },
    ),
    "video": _kind(
        "a narrated video about the sources",
        ShowFocuses,
        _SHOW_SAMPLE,
        "Explain the trial's main result to a new teacher in plain words",
        {
            "explainer": (
                "It is an explainer: each idea is a concept the narrator "
                "explains step by step, and for whom."
            ),
            "short": (
                "It is a short video: each idea is one hook or takeaway that "
                "lands in under a minute."
            ),
        },
    ),
    "infographic": _kind(
        "a one-page infographic summarising the sources",
        KeyMessages,
        [
            "Later start, more sleep: 34 minutes gained, grades up 4%",
            "Who benefits: gains by year group and by commute time",
            "The price: cost per student against days of attendance gained",
        ],
        "The one message that later starts add sleep, with the minutes gained",
    ),
}


def suggestion_kind(kind: str | None) -> tuple[str, SuggestionKind]:
    name = kind if kind in KINDS else GENERAL
    return name, KINDS[name]


def known_variant(kind: SuggestionKind, variant: str | None) -> str | None:
    return variant if variant in kind.variants else None
