"""Fit a video's source material to the authoring model's context.

A video used to see only the first 12,000 characters of its sources, so a
paper's later sections came from the model's own knowledge instead of the
paper. The full text is now given whenever it fits a budget scaled to the
model's context; otherwise every section is kept and shortened (its heading,
its opening text, its list items and tables) rather than the tail cut off, and
the model is told the material was condensed.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from app.services.context_admission import SURFSENSE_UNKNOWN_MODEL_MAX_INPUT_TOKENS

# Conservative, like context_admission's estimate: 3 characters per token.
CHARS_PER_TOKEN = 3
VIDEO_SOURCE_MAX_TOKENS = 60_000
VIDEO_SOURCE_MAX_CHARS = VIDEO_SOURCE_MAX_TOKENS * CHARS_PER_TOKEN
# The rest of the window is the prompt, the brief and a dozen TSX scenes.
_CONTEXT_SHARE = 0.4
_MIN_SOURCE_TOKENS = 2_000

CONDENSED_NOTE = (
    "The source material was condensed to fit: every section's heading is kept "
    "with its opening text, list items and tables, and longer passages are "
    "shortened (marked […]). Cover the sections from what is shown; do not "
    "fill the gaps with outside knowledge."
)

# Longest text kept per block, tried from generous to headings-only: first
# with every paragraph of a section, then with only its opening one.
_CAPS = (2_400, 1_600, 1_000, 700, 450, 300, 200, 120, 60, 0)
_ATTEMPTS = tuple((cap, True) for cap in _CAPS[:6]) + tuple(
    (cap, False) for cap in _CAPS
)
_ELLIPSIS = " […]"
_HEADING = re.compile(r"^\s{0,3}#{1,6}\s+\S")
_RUN_IN = re.compile(r"^\s*(\*\*[^*\n](?:[^*]|\*(?!\*))*?\*\*)(.*)$", re.DOTALL)
_LIST_ITEM = re.compile(r"^\s*(?:[-*+•]|\d{1,3}[.)])\s+\S")
_TABLE_ROW = re.compile(r"^\s*\|")
_BLANK_LINES = re.compile(r"\n\s*\n")


@dataclass(frozen=True)
class FittedSource:
    text: str
    condensed: bool


def source_budget_chars(max_input_tokens: int | None) -> int:
    """Characters of source a model with this context window is given."""
    if (
        not isinstance(max_input_tokens, int)
        or isinstance(max_input_tokens, bool)
        or max_input_tokens <= 0
    ):
        max_input_tokens = SURFSENSE_UNKNOWN_MODEL_MAX_INPUT_TOKENS
    tokens = min(VIDEO_SOURCE_MAX_TOKENS, int(max_input_tokens * _CONTEXT_SHARE))
    return max(tokens, _MIN_SOURCE_TOKENS) * CHARS_PER_TOKEN


def fit_source(text: str, budget_chars: int) -> FittedSource:
    """The full text when it fits, otherwise a section-aware condensation."""
    text = text.strip()
    if len(text) <= budget_chars:
        return FittedSource(text=text, condensed=False)
    sections = _sections(text)
    for cap, every_paragraph in _ATTEMPTS:
        condensed = _render(sections, cap, every_paragraph=every_paragraph)
        if len(condensed) <= budget_chars:
            return FittedSource(text=condensed, condensed=True)
    # Even the headings alone overflow: keep as many as fit.
    return FittedSource(
        text=_clip(
            _render(sections, 0, every_paragraph=False), budget_chars - len(_ELLIPSIS)
        ),
        condensed=True,
    )


@dataclass
class _Section:
    heading: str | None
    lead: str
    blocks: list[tuple[str, str]]


def _sections(text: str) -> list[_Section]:
    sections = [_Section(heading=None, lead="", blocks=[])]
    for block in _blocks(text):
        kind, value = block
        if kind == "heading":
            sections.append(_Section(heading=value, lead="", blocks=[]))
        elif kind == "run_in":
            heading, lead = value
            sections.append(_Section(heading=heading, lead=lead, blocks=[]))
        else:
            sections[-1].blocks.append(block)
    return [s for s in sections if s.heading or s.lead or s.blocks]


def _blocks(text: str):
    for raw in _BLANK_LINES.split(text):
        lines = [line.rstrip() for line in raw.strip("\n").splitlines()]
        lines = [line for line in lines if line.strip()]
        if not lines:
            continue
        if _HEADING.match(lines[0]):
            yield ("heading", lines[0].strip())
            lines = lines[1:]
            if not lines:
                continue
        if all(_TABLE_ROW.match(line) for line in lines):
            yield ("table", "\n".join(lines))
            continue
        if _LIST_ITEM.match(lines[0]):
            yield from _list_items(lines)
            continue
        paragraph = " ".join(line.strip() for line in lines)
        run_in = _RUN_IN.match(paragraph)
        if run_in and len(run_in.group(1)) <= 400:
            yield ("run_in", (run_in.group(1), run_in.group(2).strip()))
        else:
            yield ("paragraph", paragraph)


def _list_items(lines: list[str]):
    item: list[str] = []
    for line in lines:
        if _LIST_ITEM.match(line) and item:
            yield ("item", " ".join(item))
            item = []
        item.append(line.strip())
    if item:
        yield ("item", " ".join(item))


def _render(sections: list[_Section], cap: int, *, every_paragraph: bool) -> str:
    out: list[str] = []
    for section in sections:
        parts: list[str] = []
        opening = section.lead
        rest = section.blocks
        if not opening and rest and rest[0][0] == "paragraph":
            opening, rest = rest[0][1], rest[1:]
        if section.heading:
            parts.append(section.heading)
            if opening and cap:
                # A run-in heading ("**Point 3:** text") keeps its text inline.
                glue = " " if section.lead else "\n\n"
                parts[-1] += glue + _clip(opening, cap)
        elif opening and cap:
            parts.append(_clip(opening, cap))
        if cap:
            parts.extend(_body(rest, cap, every_paragraph=every_paragraph))
        if parts:
            out.append("\n\n".join(parts))
    return "\n\n".join(out)


def _body(blocks: list[tuple[str, str]], cap: int, *, every_paragraph: bool):
    """A section's remaining blocks, in order, each shortened to ``cap``."""
    items: list[str] = []
    for kind, value in blocks:
        if kind == "item":
            items.append(_clip(value, max(cap // 3, 60)))
            continue
        if items:
            yield "\n".join(items)
            items = []
        if kind == "table":
            yield _clip_table(value, cap * 2)
        elif every_paragraph:
            yield _clip(value, cap)
    if items:
        yield "\n".join(items)


def _clip(text: str, cap: int) -> str:
    if len(text) <= cap:
        return text
    if cap <= 0:
        return ""
    cut = text[:cap]
    sentence = max(cut.rfind(". "), cut.rfind("? "), cut.rfind("! "))
    if sentence >= cap // 2:
        return cut[: sentence + 1] + _ELLIPSIS
    space = cut.rfind(" ")
    return (cut[:space] if space > 0 else cut).rstrip() + _ELLIPSIS


def _clip_table(table: str, cap: int) -> str:
    if len(table) <= cap:
        return table
    kept: list[str] = []
    size = 0
    for row in table.splitlines():
        if kept and size + len(row) + 1 > cap:
            kept.append("| […] |")
            break
        kept.append(row)
        size += len(row) + 1
    return "\n".join(kept)
