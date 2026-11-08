"""The reply the report-suggestion call must satisfy.

Lenient like the Studio dialog suggestions: near-miss keys and kind names are
accepted, an over-long title or focus is cut at a word, and an item that names
no real section or an output type this server cannot make is dropped rather
than failing the reply. Only a reply with no usable suggestion is refused (and
then ``invoke_json`` hands the error back to the model once).
"""

from __future__ import annotations

import re
from typing import Any

from pydantic import (
    BaseModel,
    ConfigDict,
    ValidationInfo,
    field_validator,
    model_validator,
)

from app.utils.structured_output import flatten_to_text

from ..suggestions.schemas import clamp

REPLY_FIELD = "report_suggestions"
MAX_SUGGESTIONS = 4
TITLE_MAX_CHARS = 80
FOCUS_MAX_CHARS = 180

# What a suggestion may propose, in the order the prompt lists them. Video and
# another report are left out: both are long, costly runs better started from
# the Studio on purpose.
SUGGESTION_KINDS: tuple[str, ...] = (
    "slides",
    "flashcards",
    "quiz",
    "mindmap",
    "audio",
    "infographic",
    "data_table",
)

_KIND_ALIASES = {
    "slide deck": "slides",
    "slide_deck": "slides",
    "slidedeck": "slides",
    "deck": "slides",
    "presentation": "slides",
    "flash cards": "flashcards",
    "flash_cards": "flashcards",
    "cards": "flashcards",
    "mind map": "mindmap",
    "mind_map": "mindmap",
    "audio overview": "audio",
    "audio_overview": "audio",
    "podcast": "audio",
    "data table": "data_table",
    "datatable": "data_table",
    "table": "data_table",
    "multiple choice quiz": "quiz",
}
_QUOTES = "\"'\u201c\u201d\u2018\u2019\u00ab\u00bb\u300c\u300d"
_LIST_ALIASES = ("suggestions", "items", "ideas", "outputs", "recommendations")


def normalise_kind(value: Any) -> str:
    text = " ".join(str(value or "").casefold().replace("-", " ").split())
    return _KIND_ALIASES.get(text, text.replace(" ", "_"))


def _prose(value: Any, limit: int) -> str:
    text = " ".join(flatten_to_text(value).split()).strip(_QUOTES + " ")
    return clamp(text[:1].upper() + text[1:], limit)


class ReportSuggestion(BaseModel):
    model_config = ConfigDict(extra="ignore")

    # 1-based, as the prompt numbers the sections.
    section: int
    kind: str
    title: str
    focus: str = ""

    @model_validator(mode="before")
    @classmethod
    def _near_miss_keys(cls, data: Any) -> Any:
        if not isinstance(data, dict):
            return data
        data = dict(data)
        for want, aliases in (
            ("section", ("section_number", "section_index", "after_section")),
            ("kind", ("type", "output", "output_type", "format")),
            ("focus", ("description", "summary", "why", "detail")),
        ):
            if want not in data:
                alias = next((a for a in aliases if a in data), None)
                if alias is not None:
                    data[want] = data.pop(alias)
        return data

    @field_validator("section", mode="before")
    @classmethod
    def _section_number(cls, value: Any) -> Any:
        if isinstance(value, str):
            match = re.search(r"\d+", value)
            return int(match.group()) if match else value
        return value

    @field_validator("kind", mode="before")
    @classmethod
    def _kind(cls, value: Any) -> str:
        return normalise_kind(value)

    @field_validator("title", mode="before")
    @classmethod
    def _title(cls, value: Any) -> str:
        return _prose(value, TITLE_MAX_CHARS)

    @field_validator("focus", mode="before")
    @classmethod
    def _focus(cls, value: Any) -> str:
        return _prose(value, FOCUS_MAX_CHARS)


class ReportSuggestionsReply(BaseModel):
    """Validated with context ``{"sections": n, "kinds": [...]}``."""

    model_config = ConfigDict(extra="ignore")

    # Raw items in; only the usable ReportSuggestion items once validated.
    report_suggestions: list[Any]

    @model_validator(mode="before")
    @classmethod
    def _list_key(cls, data: Any) -> Any:
        if isinstance(data, list):
            return {REPLY_FIELD: data}
        if isinstance(data, dict) and REPLY_FIELD not in data:
            alias = next((k for k in _LIST_ALIASES if k in data), None)
            if alias is not None:
                return {REPLY_FIELD: data[alias]}
        return data

    @field_validator(REPLY_FIELD, mode="before")
    @classmethod
    def _items(cls, value: Any) -> Any:
        if isinstance(value, dict):
            return list(value.values())
        return value

    @model_validator(mode="after")
    def _usable(self, info: ValidationInfo) -> ReportSuggestionsReply:
        context = info.context or {}
        sections = int(context.get("sections") or 0)
        kinds = set(context.get("kinds") or SUGGESTION_KINDS)
        kept: list[ReportSuggestion] = []
        used_sections: set[int] = set()
        used_kinds: set[str] = set()
        for raw in self.report_suggestions:
            try:
                item = ReportSuggestion.model_validate(raw)
            except ValueError:
                continue
            if not item.title or item.kind not in kinds:
                continue
            if sections and not 1 <= item.section <= sections:
                continue
            if item.section in used_sections or item.kind in used_kinds:
                continue
            used_sections.add(item.section)
            used_kinds.add(item.kind)
            kept.append(item)
            if len(kept) >= MAX_SUGGESTIONS:
                break
        if not kept:
            raise ValueError(
                "need at least one suggestion naming a listed section number "
                f"and one of these kinds: {', '.join(sorted(kinds))}"
            )
        self.report_suggestions = kept
        return self

    def suggestions(self) -> list[ReportSuggestion]:
        return list(self.report_suggestions)
