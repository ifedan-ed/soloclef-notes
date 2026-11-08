"""The reply each Studio dialog's suggestion call must satisfy.

One schema per family of outputs, because "a table design" and "what the
hosts should argue about" are different things to ask for, and a field named
for what it holds is itself an instruction. Every schema shares the same
leniency: an over-long idea is cut at a word, an object or list is flattened
to prose, a generic "Focus on ..." opener is dropped, and duplicates are
removed. Only a reply with fewer than three usable ideas is refused, and then
``invoke_json`` hands the error back to the model once.
"""

from __future__ import annotations

import re
from typing import Annotated, Any, ClassVar

from pydantic import BaseModel, BeforeValidator, ConfigDict, Field, model_validator

from app.utils.structured_output import flatten_to_text

CHIP_MAX_CHARS = 90
PLACEHOLDER_MAX_CHARS = 140
# The dialog shows this many at a time, from a pool generated in one call.
WINDOW = 3
POOL_SIZE = 9
EXAMPLES = 3

_ELLIPSIS = "…"
# Openers that make every chip read alike; the substance follows them.
_OPENER = re.compile(
    r"^(?:(?:please|try\s+to|let'?s)\s+)?"
    r"(?:(?:focus(?:ing)?|concentrate|centre|center|zoom\s+in)\s+(?:on|upon)\b"
    r"|focus\s*[:\-\u2013\u2014])[\s:,\-\u2013\u2014]*",
    re.IGNORECASE,
)
_EXAMPLE_LEAD = re.compile(
    r"^(?:e\.\s?g\.|for\s+example|example)[\s:,\-\u2013\u2014]*", re.I
)
_LIST_MARK = re.compile(r"^(?:[-*•·]|\d{1,2}[.)])\s+")
_QUOTES = "\"'\u201c\u201d\u2018\u2019\u00ab\u00bb\u300c\u300d"


def clamp(text: str, limit: int) -> str:
    """``text`` within ``limit`` characters, cut at a word and marked as cut."""
    if len(text) <= limit:
        return text
    cut = text[: limit - len(_ELLIPSIS)]
    space = cut.rfind(" ")
    if space >= limit * 0.6:
        cut = cut[:space]
    return cut.rstrip(" ,;:-\u2013\u2014") + _ELLIPSIS


def _prose(value: Any) -> str:
    text = " ".join(flatten_to_text(value).split())
    return _LIST_MARK.sub("", text).strip(_QUOTES + " ")


def _capitalised(text: str) -> str:
    return text[:1].upper() + text[1:] if text[:1].islower() else text


def normalise_idea(value: Any) -> str:
    text = _prose(value)
    while (stripped := _OPENER.sub("", text, count=1)) != text:
        text = stripped
    return clamp(_capitalised(text.strip(_QUOTES + " ")), CHIP_MAX_CHARS)


def normalise_example(value: Any) -> str:
    text = _EXAMPLE_LEAD.sub("", _prose(value), count=1)
    while (stripped := _OPENER.sub("", text, count=1)) != text:
        text = stripped
    return clamp(_capitalised(text.strip(_QUOTES + " ")), PLACEHOLDER_MAX_CHARS)


def _as_list(value: Any) -> Any:
    if isinstance(value, str | int | float):
        return [value]
    if isinstance(value, dict):
        return list(value.values())
    return value


def distinct(values: list[str], *, minimum: int, maximum: int, what: str) -> list[str]:
    """The first ``maximum`` distinct non-empty values; too few is an error."""
    seen: set[str] = set()
    kept: list[str] = []
    for value in values:
        key = re.sub(r"\W+", "", value.casefold())
        if key and key not in seen:
            seen.add(key)
            kept.append(value)
    if len(kept) < minimum:
        raise ValueError(
            f"need at least {minimum} distinct, non-empty {what}; got {len(kept)}"
        )
    return kept[:maximum]


Idea = Annotated[str, BeforeValidator(normalise_idea)]
Example = Annotated[str, BeforeValidator(normalise_example)]
Ideas = Annotated[list[Idea], BeforeValidator(_as_list)]
Examples = Annotated[list[Example], BeforeValidator(_as_list)]

# Keys a model reaches for instead of the schema's own field name.
_IDEA_ALIASES = ("ideas", "suggestions", "chips", "topics", "items", "options")
_EXAMPLE_ALIASES = ("example", "placeholder", "placeholders", "example_prompts")


class IdeaPool(BaseModel):
    """A pool of ideas for one dialog, plus examples for its topic box."""

    model_config = ConfigDict(extra="ignore")

    ideas_field: ClassVar[str]

    @model_validator(mode="before")
    @classmethod
    def _accept_near_miss_keys(cls, data: Any) -> Any:
        if isinstance(data, list):
            data = {cls.ideas_field: data}
        if not isinstance(data, dict):
            return data
        data = dict(data)
        if cls.ideas_field not in data:
            alias = next((k for k in _IDEA_ALIASES if k in data), None)
            if alias is not None:
                data[cls.ideas_field] = data.pop(alias)
        if "examples" not in data:
            alias = next((k for k in _EXAMPLE_ALIASES if k in data), None)
            data["examples"] = data.pop(alias) if alias is not None else []
        return data

    @model_validator(mode="after")
    def _distinct_pool(self) -> IdeaPool:
        field = self.ideas_field
        setattr(
            self,
            field,
            distinct(
                getattr(self, field), minimum=WINDOW, maximum=POOL_SIZE, what=field
            ),
        )
        # An example is a nicety: with none usable the dialog keeps its own.
        # One that repeats a chip word for word adds nothing, so it goes.
        chips = {idea.casefold() for idea in getattr(self, field)}
        self.examples = distinct(
            [e for e in self.examples if e.casefold() not in chips],
            minimum=0,
            maximum=EXAMPLES,
            what="examples",
        )
        return self

    def ideas(self) -> list[str]:
        return list(getattr(self, self.ideas_field))


class TableDesigns(IdeaPool):
    ideas_field: ClassVar[str] = "designs"
    designs: Ideas = Field(
        description=(
            "Table designs. Each names the row unit and the columns, like "
            "'One row per <thing>: <column>, <column>, <column>'."
        )
    )
    examples: Examples = Field(
        description="What the user might type to ask for one such table."
    )


class StudyTargets(IdeaPool):
    ideas_field: ClassVar[str] = "targets"
    targets: Ideas = Field(
        description=(
            "What to test: which concepts, at what level, and in what question style."
        )
    )
    examples: Examples = Field(
        description="What the user might type to say what should be tested."
    )


class MapOutlines(IdeaPool):
    ideas_field: ClassVar[str] = "outlines"
    outlines: Ideas = Field(
        description=(
            "Mind map outlines. Each gives the central idea and its main "
            "branches, like '<central idea>: <branch>, <branch>, <branch>'."
        )
    )
    examples: Examples = Field(
        description="What the user might type to say what the map centers on."
    )


class Angles(IdeaPool):
    ideas_field: ClassVar[str] = "angles"
    angles: Ideas = Field(
        description=(
            "Angles. Each names an audience, an angle and a scope, like "
            "'For <audience>: <angle>, limited to <scope>'."
        )
    )
    examples: Examples = Field(
        description="What the user might type to set the audience and angle."
    )


class ShowFocuses(IdeaPool):
    ideas_field: ClassVar[str] = "focuses"
    focuses: Ideas = Field(
        description="What the hosts or narrator should spend their time on."
    )
    examples: Examples = Field(
        description="What the user might type to steer the episode."
    )


class KeyMessages(IdeaPool):
    ideas_field: ClassVar[str] = "messages"
    messages: Ideas = Field(
        description=(
            "Infographic messages. Each states the one message and the key "
            "figures to show, like '<message>: <figure>, <figure>'."
        )
    )
    examples: Examples = Field(
        description="What the user might type to say what the infographic shows."
    )


class Questions(IdeaPool):
    ideas_field: ClassVar[str] = "questions"
    questions: Ideas = Field(
        description=(
            "Questions to ask in a chat about the sources, each answerable "
            "from them, written as the user would type it and ending in '?'."
        )
    )
    examples: Examples = Field(description="Another question the user might ask.")


class Topics(IdeaPool):
    ideas_field: ClassVar[str] = "topics"
    topics: Ideas = Field(description="Specific topics worth an overview.")
    examples: Examples = Field(
        description="What the user might type to steer the overview."
    )
